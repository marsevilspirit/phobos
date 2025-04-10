package serverplugin

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marsevilspirit/phobos/log"
	metrics "github.com/rcrowley/go-metrics"
	"github.com/rpcxio/libkv"
	"github.com/rpcxio/libkv/store"
	estore "github.com/rpcxio/rpcx-etcd/store"
	"github.com/rpcxio/rpcx-etcd/store/etcdv3"
)

func init() {
	libkv.AddStore(estore.ETCDV3, etcdv3.New)
}

// EtcdRegisterPlugin implements etcd registry.
type EtcdRegisterPlugin struct {
	ServiceAddress string

	EtcdServers []string

	BasePath string
	Metrics  metrics.Registry

	Services       []string
	metasLock      sync.RWMutex
	metaMap        map[string]string
	UpdateInterval time.Duration

	kv store.Store
}

// Start starts to connect etcd cluster
func (p *EtcdRegisterPlugin) Start() error {
	if p.kv == nil {
		kv, err := libkv.NewStore(estore.ETCDV3, p.EtcdServers, nil)
		if err != nil {
			log.Errorf("cannot create etcd registry: %v", err)
			return err
		}
		p.kv = kv
	}

	err := p.kv.Put(p.BasePath, []byte("mrpc_path"), &store.WriteOptions{IsDir: true})
	if err != nil && !strings.Contains(err.Error(), "Not a file") {
		log.Errorf("cannot create etcd path %s: %v", p.BasePath, err)
		return err
	}

	if p.UpdateInterval > 0 {
		ticker := time.NewTicker(p.UpdateInterval)
		go func() {
			defer p.kv.Close()

			// refresh service TTL
			for range ticker.C {
				var data []byte

				if p.Metrics != nil {
					clientMeter := metrics.GetOrRegisterMeter("clientMeter", p.Metrics)
					data = []byte(strconv.FormatInt(clientMeter.Count()/60, 10))
				}
				//set this same metrics for all services at this server
				for _, name := range p.Services {
					nodePath := fmt.Sprintf("%s/%s/%s", p.BasePath, name, p.ServiceAddress)
					kvPair, err := p.kv.Get(nodePath)
					if err != nil {
						log.Infof("can't get data of node: %s, because of %v", nodePath, err.Error())

						p.metasLock.RLock()
						metadata := p.metaMap[name]
						p.metasLock.RUnlock()

						err = p.kv.Put(nodePath, []byte(metadata), &store.WriteOptions{TTL: p.UpdateInterval * 3})
						if err != nil {
							log.Errorf("cannot create etcd path %s: %v", nodePath, err)
						}
					} else {
						v, _ := url.ParseQuery(string(kvPair.Value))
						v.Set("tps", string(data))
						p.kv.Put(nodePath, []byte(v.Encode()), &store.WriteOptions{TTL: p.UpdateInterval * 3})
					}
				}

			}
		}()
	}

	return nil
}

// HandleConnAccept handles connections from clients
func (p *EtcdRegisterPlugin) HandleConnAccept(conn net.Conn) (net.Conn, bool) {
	if p.Metrics != nil {
		clientMeter := metrics.GetOrRegisterMeter("clientMeter", p.Metrics)
		clientMeter.Mark(1)
	}
	return conn, true
}

// Register handles registering event.
// this service is registered at BASE/serviceName/thisIpAddress node
func (p *EtcdRegisterPlugin) Register(name string, rcvr interface{}, metadata string) (err error) {
	if "" == strings.TrimSpace(name) {
		err = errors.New("Register service `name` can't be empty")
		return
	}

	if p.kv == nil {
		kv, err := libkv.NewStore(estore.ETCDV3, p.EtcdServers, nil)
		if err != nil {
			log.Errorf("cannot create etcd registry: %v", err)
			return err
		}
		p.kv = kv
	}

	err = p.kv.Put(p.BasePath, []byte("mrpc_path"), &store.WriteOptions{IsDir: true})
	if err != nil && !strings.Contains(err.Error(), "Not a file") {
		log.Errorf("cannot create etcd path %s: %v", p.BasePath, err)
		return err
	}

	nodePath := fmt.Sprintf("%s/%s", p.BasePath, name)
	err = p.kv.Put(nodePath, []byte(name), &store.WriteOptions{IsDir: true})
	if err != nil && !strings.Contains(err.Error(), "Not a file") {
		log.Errorf("cannot create etcd path %s: %v", nodePath, err)
		return err
	}

	nodePath = fmt.Sprintf("%s/%s/%s", p.BasePath, name, p.ServiceAddress)
	err = p.kv.Put(nodePath, []byte(metadata), &store.WriteOptions{TTL: p.UpdateInterval * 2})
	if err != nil {
		log.Errorf("cannot create etcd path %s: %v", nodePath, err)
		return err
	}

	p.Services = append(p.Services, name)

	p.metasLock.Lock()
	if p.metaMap == nil {
		p.metaMap = make(map[string]string)
	}
	p.metaMap[name] = metadata
	p.metasLock.Unlock()

	return
}
