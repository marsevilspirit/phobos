package util

import (
	"testing"
)

func TestZip(t *testing.T) {
	s := "%5B%7B%22service%22%3A%22AttrDict%22%2C%22service_address%22%3A%22udp%40127.0.0.1%3A5353%22%7D%2C%7B%22service%22%3A%22BrasInfo%22%2C%22service_address%22%3A%22udp%40127.0.0.1%3A5353%22%7D%5D"
	t.Logf("origin len: %d", len(s))
	data, err := Zip([]byte(s))
	if err != nil {
		t.Fatalf("failed to zip: %v", err)
	}
	t.Logf("zipped len: %d", len(data))
	s2, err := Unzip(data)
	if err != nil {
		t.Fatalf("failed to unzip: %v", err)
	}

	if string(s2) != s {
		t.Fatalf("unzip data is wrong")
	}
}
