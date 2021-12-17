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
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/kitex/pkg/klog"
	"github.com/cloudwego/kitex/pkg/remote"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/grpc"
	"github.com/cloudwego/netpoll"
	"golang.org/x/sync/singleflight"
)

var _ remote.LongConnPool = &connPool{}

func poolSize() int32 {
	numP := runtime.GOMAXPROCS(0)
	fmt.Println("CPU: ", int32(numP * 3 / 2))
	return int32(numP * 3 / 2)
}

// NewConnPool ...
func NewConnPool(remoteService string) *connPool {
	return &connPool{
		size:          poolSize(),
		remoteService: remoteService,
	}
}

// MuxPool manages a pool of long connections.
type connPool struct {
	size  int32
	sfg   singleflight.Group
	conns sync.Map // key: address, value: *transports

	remoteService string // remote service name
}

type transports struct {
	index         int32
	size          int32
	cliTransports []grpc.ClientTransport
}

func (t *transports) get() grpc.ClientTransport {
	idx := atomic.AddInt32(&t.index, 1)
	return t.cliTransports[idx%t.size]
}

func (t *transports) put(trans grpc.ClientTransport) {
	for i := 0; i < int(t.size); i++ {
		if t.cliTransports[i] == nil {
			t.cliTransports[i] = trans
			return
		}
		if !t.cliTransports[i].(grpc.GetConn).GetRawConn().IsActive() {
			t.cliTransports[i].GracefulClose()
			t.cliTransports[i] = trans
			return
		}
	}
}

func (c *transports) close() {
	for i := range c.cliTransports {
		if c.cliTransports[i] != nil {
			c.cliTransports[i].GracefulClose()
		}
	}
}

var _ remote.LongConnPool = (*connPool)(nil)

func (p *connPool) newTransport(ctx context.Context, dialer remote.Dialer, network, address string, connectTimeout time.Duration) (grpc.ClientTransport, error) {
	conn, err := dialer.DialTimeout(network, address, connectTimeout)
	if err != nil {
		return nil, err
	}
	return grpc.NewClientTransport(
		ctx,
		conn.(netpoll.Connection),
		p.remoteService,
		func(grpc.GoAwayReason) {
			// do nothing
		},
		func() {
			// do nothing
		},
	)
}

// Get pick or generate a net.Conn and return
func (p *connPool) Get(ctx context.Context, network, address string, opt remote.ConnOption) (net.Conn, error) {
	var (
		trans *transports
		conn  *clientConn
		err   error
	)

	v, ok := p.conns.Load(address)
	if ok {
		trans = v.(*transports)
		if tr := trans.get(); tr != nil {
			rawConn := tr.(grpc.GetConn).GetRawConn()
			if rawConn.IsActive() {
				// Actually new a stream, reuse the connection (grpc.ClientTransport)
				conn, err = newClientConn(ctx, tr, address)
				if err == nil {
					return conn, nil
				}
				klog.CtxInfof(ctx, "KITEX: New grpc stream failed, network=%s, address=%s, error=%s", network, address, err.Error())
			}
		}
	}
	tr, err, _ := p.sfg.Do(address, func() (i interface{}, e error) {
		// Notice: newTransport means new a connection, the timeout of connection cannot be set,
		// so using context.Background() but not the ctx passed in as the parameter.
		tr, err := p.newTransport(context.Background(), opt.Dialer, network, address, opt.ConnectTimeout)
		if err != nil {
			return nil, err
		}
		if trans == nil {
			trans = &transports{
				size:          p.size,
				cliTransports: make([]grpc.ClientTransport, p.size),
			}
		}
		trans.put(tr)
		p.conns.Store(address, trans)
		return tr, nil
	})
	if err != nil {
		klog.CtxInfof(ctx, "KITEX: New grpc client connection failed, network=%s, address=%s, error=%s", network, address, err.Error())
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
