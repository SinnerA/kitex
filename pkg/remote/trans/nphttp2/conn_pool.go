/*
 * Copyright 2021 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package nphttp2

import (
	"context"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/netpoll"

	"github.com/cloudwego/kitex/pkg/remote"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/grpc"

	"golang.org/x/sync/singleflight"
)

func poolSize() int32 {
	defaultSize := int32(4)
	if val := os.Getenv("GRPC_POOL_SIZE"); val != "" {
		size, err := strconv.Atoi(val)
		if err != nil {
			return defaultSize
		}
		return int32(size)
	}
	return defaultSize
}

// NewConnPool ...
func NewConnPool() remote.LongConnPool {
	return &connPool{
		size: poolSize(),
	}
}

// MuxPool manages a pool of long connections.
type connPool struct {
	size  int32
	sfg   singleflight.Group
	conns sync.Map // key: address, value: *transports
}

type transports struct {
	index      int32
	size       int32
	transports []grpc.ClientTransport
}

func (t *transports) get() grpc.ClientTransport {
	idx := atomic.AddInt32(&t.index, 1)
	return t.transports[idx%t.size]
}

func (t *transports) put(trans grpc.ClientTransport) {
	for i := 0; i < int(t.size); i++ {
		if t.transports[i] == nil {
			t.transports[i] = trans
			return
		}
	}
}

func (t *transports) reset() {
	curIdx := atomic.LoadInt32(&t.index)
	t.transports[curIdx%t.size] = nil
}

func (c *transports) close() {
	for i := range c.transports {
		if c.transports[i] != nil {
			c.transports[i].GracefulClose()
		}
	}
}

var _ remote.LongConnPool = (*connPool)(nil)

func newTransport(dialer remote.Dialer, network, address string, connectTimeout time.Duration) (grpc.ClientTransport, error) {
	conn, err := dialer.DialTimeout(network, address, connectTimeout)
	if err != nil {
		return nil, err
	}
	return grpc.NewClientTransport(
		context.Background(),
		conn.(netpoll.Connection),
		nil,
		nil,
	)
}

// Get pick or generate a net.Conn and return
func (p *connPool) Get(ctx context.Context, network, address string, opt *remote.ConnOption) (net.Conn, error) {
	var (
		trans *transports
		conn  *clientConn
		err   error
	)

	v, ok := p.conns.Load(address)
	if ok {
		trans = v.(*transports)
		if tr := trans.get(); tr != nil {
			conn, err = newClientConn(ctx, tr, address)
			if err != nil {
				tr.GracefulClose()
				trans.reset()
				return nil, err
			}
			return conn, nil
		}
	}
	tr, err, _ := p.sfg.Do(address, func() (i interface{}, e error) {
		tr, err := newTransport(opt.Dialer, network, address, opt.ConnectTimeout)
		if err != nil {
			return nil, err
		}
		if trans == nil {
			trans = &transports{
				size:       p.size,
				transports: make([]grpc.ClientTransport, p.size),
			}
		}
		trans.put(tr)
		p.conns.Store(address, trans)
		return tr, nil
	})
	if err != nil {
		return nil, err
	}
	return newClientConn(ctx, tr.(grpc.ClientTransport), address)
}

// Put implements the ConnPool interface.
func (p *connPool) Put(conn net.Conn) error {
	return nil
}

// Discard implements the ConnPool interface.
func (p *connPool) Discard(conn net.Conn) error {
	return nil
}

// Clean implements the LongConnPool interface.
func (p *connPool) Clean(network, address string) {
	if v, ok := p.conns.Load(address); ok {
		p.conns.Delete(address)
		v.(*transports).close()
	}
}

// Close is to release resource of ConnPool, it is executed when client is closed.
func (p *connPool) Close() error {
	p.conns.Range(func(addr, trans interface{}) bool {
		p.conns.Delete(addr)
		trans.(*transports).close()
		return true
	})
	return nil
}
