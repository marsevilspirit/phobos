package client

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	ex "github.com/marsevilspirit/m_RPC/errors"
	"github.com/marsevilspirit/m_RPC/share"
)

var (
	ErrXClientShutdown   = errors.New("xClient is shut down")
	ErrXClientNoServer   = errors.New("xClient can not found any server")
	ErrServerUnavailable = errors.New("selected server is unavilable")
)

type XClient interface {
	SetPlugins(plugins PluginContainer)
	SetGeoSelector(latitude, longitude float64)
	Auth(auth string)
	Go(ctx context.Context, args, reply interface{}, metadata map[string]string, done chan *Call) (*Call, error)
	Call(ctx context.Context, args, reply interface{}, metadata map[string]string) error
	Broadcast(ctx context.Context, args, reply interface{}, metadata map[string]string) error
	Fork(ctx context.Context, args, reply interface{}, metadata map[string]string) error
	Close() error
}

type KVPair struct {
	Key   string
	Value string
}

// ServiceDiscovery 接口定义，用于服务发现
type ServiceDiscovery interface {
	// 获取所有服务
	GetServices() []*KVPair
	// 监听服务变化，返回服务变化的通道
	WatchService() chan []*KVPair
}

// xClient 结构体实现 XClient 接口
type xClient struct {
	failMode     FailMode           // 失败处理模式
	selectMode   SelectMode         // 选择处理模式
	cachedClient map[string]*Client // 缓存的客户端连接

	mu        sync.RWMutex      // 读写锁，用于保护共享资源的并发访问
	servers   map[string]string // 当前已知的服务器地址
	discovery ServiceDiscovery  // 服务发现接口
	selector  Selector
	option    Option

	servicePath   string
	serviceMethod string

	isShutdown bool // 客户端是否已关闭的标志

	auth string

	// Latitude  float64
	// Longitude float64

	Plugins PluginContainer
}

// NewXClient 工厂函数，用于创建 xClient 实例
func NewXClient(servicePath, serviceMethod string, failMode FailMode, selectMode SelectMode, discovery ServiceDiscovery, option Option) XClient {
	// 初始化 xClient 结构体
	client := &xClient{
		failMode:      failMode,
		selectMode:    selectMode,
		discovery:     discovery,
		servicePath:   servicePath,
		serviceMethod: serviceMethod,
		cachedClient:  make(map[string]*Client),
		option:        option,
	}

	ch := client.discovery.WatchService()
	if ch != nil {
		go client.watch(ch)
	}

	// 更新服务列表
	servers := make(map[string]string)
	pairs := discovery.GetServices()
	for _, p := range pairs {
		servers[p.Key] = p.Value
	}
	client.servers = servers
	if selectMode != Closest {
		client.selector = newSelector(selectMode, servers)
	}

	client.Plugins = &pluginContainer{}

	return client
}

func (c *xClient) SetPlugins(plugins PluginContainer) {
	c.Plugins = plugins
}

func (c *xClient) SetGeoSelector(latitude, longitude float64) {
	c.selector = newGeoSelector(c.servers, latitude, longitude)
}

func (c *xClient) Auth(auth string) {
	c.auth = auth
}

// watch 方法，用于不断监听服务变化并更新服务器列表
func (c *xClient) watch(ch chan []*KVPair) {
	for pairs := range ch {
		servers := make(map[string]string)
		for _, p := range pairs {
			servers[p.Key] = p.Value
		}
		c.mu.Lock()
		c.servers = servers
		c.mu.Unlock()
	}
}

// selectClient 方法，用于根据选择模式选择客户端
func (c *xClient) selectClient(ctx context.Context, servicePath, serviceMethod string, args interface{}) (string, *Client, error) {
	k := c.selector.Select(ctx, servicePath, serviceMethod, args)
	if k == "" {
		return "", nil, ErrXClientNoServer
	}

	client, err := c.getCachedClient(k)

	return k, client, err
}

// getCachedClient 方法，根据服务器键获取缓存的客户端连接
func (c *xClient) getCachedClient(k string) (*Client, error) {
	c.mu.RLock()
	client := c.cachedClient[k]
	if client != nil {
		if !client.closing && !client.shutdown {
			c.mu.RUnlock()
			return client, nil
		}
	}
	c.mu.RUnlock()

	// 双检查，确保线程安全
	c.mu.Lock()
	client = c.cachedClient[k]
	if client == nil {
		network, addr := splitNetworkAndAddress(k)
		client = &Client{
			option:  c.option,
			Plugins: c.Plugins,
		}
		err := client.Connect(network, addr)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		c.cachedClient[k] = client
	}
	c.mu.Unlock()

	return client, nil
}

// splitNetworkAndAddress 方法，用于分割服务器地址
func splitNetworkAndAddress(server string) (string, string) {
	ss := strings.SplitN(server, "@", 2)
	if len(ss) == 1 {
		return "tcp", server
	}

	return ss[0], ss[1]
}

func (c *xClient) wrapCall(ctx context.Context, client *Client, args interface{}, reply interface{}, metadata map[string]string) error {
	if client == nil {
		return ErrServerUnavailable
	}

	c.Plugins.DoPreCall(ctx, c.servicePath, c.serviceMethod, args, metadata)
	err := client.call(ctx, c.servicePath, c.serviceMethod, args, reply, metadata)
	c.Plugins.DoPostCall(ctx, c.servicePath, c.serviceMethod, args, reply, metadata, err)
	return err
}

// Go 方法实现异步调用 RPC
func (c *xClient) Go(ctx context.Context, args, reply interface{}, metadata map[string]string, done chan *Call) (*Call, error) {
	if c.isShutdown {
		return nil, ErrXClientShutdown
	}

	if c.auth != "" {
		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata[share.AuthKey] = c.auth
	}

	_, client, err := c.selectClient(ctx, c.servicePath, c.serviceMethod, args)
	if err != nil {
		return nil, err
	}

	return client.Go(ctx, c.servicePath, c.serviceMethod, args, reply, metadata, done), nil
}

// Call 方法实现同步调用 RPC，通过调用 Go 方法并等待结果
func (c *xClient) Call(ctx context.Context, args, reply interface{}, metadata map[string]string) error {
	if c.isShutdown {
		return ErrXClientShutdown
	}

	if c.auth != "" {
		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata[share.AuthKey] = c.auth
	}

	var err error

	k, client, err := c.selectClient(ctx, c.servicePath, c.serviceMethod, args)
	if err != nil {
		return err
	}

	switch c.failMode {
	case Failtry:
		retries := c.option.Retries
		for retries > 0 {
			retries--
			err = c.wrapCall(ctx, client, args, reply, metadata)
			if err == nil {
				return nil
			}
			client, err = c.getCachedClient(k)
		}
		return err
	case Failover:
		retries := c.option.Retries
		for retries > 0 {
			retries--
			err = c.wrapCall(ctx, client, args, reply, metadata)
			if err == nil {
				return nil
			}

			k, client, err = c.selectClient(ctx, c.servicePath, c.serviceMethod, args)
		}

		return err
	default: // Failfast
		return c.wrapCall(ctx, client, args, reply, metadata)
	}
}

func (c *xClient) Broadcast(ctx context.Context, args, reply interface{}, metadata map[string]string) error {
	if c.isShutdown {
		return ErrXClientShutdown
	}

	if c.auth != "" {
		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata[share.AuthKey] = c.auth
	}

	var clients []*Client

	c.mu.RLock()
	for k := range c.servers {
		client, err := c.getCachedClient(k)
		if err != nil {
			c.mu.RUnlock()
			return err
		}
		clients = append(clients, client)
	}
	c.mu.RUnlock()

	if len(clients) == 0 {
		return ErrXClientNoServer
	}

	var err error
	l := len(clients)
	done := make(chan bool, l)
	for _, client := range clients {
		client := client
		go func() {
			err = c.wrapCall(ctx, client, args, reply, metadata)
			done <- (err == nil)
		}()
	}

	timeout := time.After(time.Minute)

check:
	for {
		select {
		case result := <-done:
			l--
			if l == 0 || !result {
				break check
			}
		case <-timeout:
			break check
		}
	}

	return err
}

func (c *xClient) Fork(ctx context.Context, args, reply interface{}, metadata map[string]string) error {
	if c.isShutdown {
		return ErrXClientShutdown
	}

	if c.auth != "" {
		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata[share.AuthKey] = c.auth
	}

	var clients []*Client

	c.mu.RLock()
	for k := range c.servers {
		client, err := c.getCachedClient(k)
		if err != nil {
			c.mu.RUnlock()
			return err
		}

		clients = append(clients, client)
	}
	c.mu.RUnlock()

	if len(clients) == 0 {
		return ErrXClientNoServer
	}

	var err error
	l := len(clients)
	done := make(chan bool, l)
	for _, client := range clients {
		client := client
		go func() {
			// 代码中只有在调用成功（err == nil）时才会更新原始的 reply 这样可以确保只有成功的调用结果才会被保存
			clonedReply := reflect.New(reflect.ValueOf(reply).Elem().Type()).Interface()
			err = c.wrapCall(ctx, client, args, clonedReply, metadata)
			done <- (err == nil)
			if err == nil {
				reflect.ValueOf(reply).Set(reflect.ValueOf(clonedReply))
			}
			return
		}()
	}

	timeout := time.After(time.Minute)

check:
	for {
		select {
		case result := <-done:
			l--
			if result {
				return nil
			}
			if l == 0 { // all returns or some one returns an error
				break check
			}

		case <-timeout:
			break check
		}
	}

	return err
}

// Close 方法关闭客户端，释放资源
func (c *xClient) Close() error {
	c.isShutdown = true

	var errs []error
	c.mu.Lock()
	for _, v := range c.cachedClient {
		e := v.Close()
		if e != nil {
			errs = append(errs, e)
		}
	}
	c.mu.Unlock()

	if len(errs) > 0 {
		return ex.NewMultiError(errs)
	}
	return nil
}
