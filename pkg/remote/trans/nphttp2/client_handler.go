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
	"time"

	"github.com/cloudwego/kitex/pkg/klog"

	"github.com/cloudwego/kitex/pkg/remote"
	"github.com/cloudwego/kitex/pkg/remote/codec/protobuf"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/metadata"
)

type cliTransHandlerFactory struct{}

// NewCliTransHandlerFactory ...
func NewCliTransHandlerFactory() remote.ClientTransHandlerFactory {
	return &cliTransHandlerFactory{}
}

func (f *cliTransHandlerFactory) NewTransHandler(opt *remote.ClientOption) (remote.ClientTransHandler, error) {
	return newCliTransHandler(opt)
}

func newCliTransHandler(opt *remote.ClientOption) (*cliTransHandler, error) {
	return &cliTransHandler{
		opt:   opt,
		codec: protobuf.NewGRPCCodec(),
	}, nil
}

var _ remote.ClientTransHandler = &cliTransHandler{}

type cliTransHandler struct {
	opt   *remote.ClientOption
	codec remote.Codec
}

func (h *cliTransHandler) Write(ctx context.Context, conn net.Conn, msg remote.Message) (err error) {
	now := time.Now()
	deadline, _ := ctx.Deadline()
	klog.CtxInfof(ctx, "KITEX: http2 cliTransHandler Write start, timestamp: %s, leftTime: %s", now.String(), deadline.Sub(now).String())
	defer func() {
		now := time.Now()
		deadline, _ := ctx.Deadline()
		klog.CtxInfof(ctx, "KITEX: http2 cliTransHandler Write end, timestamp: %s, leftTime: %s", now.String(), deadline.Sub(now).String())
	}()
	buf := newBuffer(conn)
	defer buf.Release(err)

	if err = h.codec.Encode(ctx, msg, buf); err != nil {
		return err
	}
	return buf.Flush()
}

func (h *cliTransHandler) Read(ctx context.Context, conn net.Conn, msg remote.Message) (err error) {
	now := time.Now()
	deadline, _ := ctx.Deadline()
	klog.CtxInfof(ctx, "KITEX: http2 cliTransHandler Read start, timestamp: %s, leftTime: %s", now.String(), deadline.Sub(now).String())
	defer func() {
		now := time.Now()
		deadline, _ := ctx.Deadline()
		klog.CtxInfof(ctx, "KITEX: http2 cliTransHandler Read end, timestamp: %s, leftTime: %s", now.String(), deadline.Sub(now).String())
	}()
	buf := newBuffer(conn)
	defer buf.Release(err)
	err = h.codec.Decode(ctx, msg, buf)
	return
}

func (h *cliTransHandler) OnRead(ctx context.Context, conn net.Conn) (context.Context, error) {
	// do nothing
	hdr, err := conn.(*clientConn).Header()
	if err != nil {
		return nil, err
	}

	return metadata.NewIncomingContext(ctx, hdr), nil
}

func (h *cliTransHandler) OnConnect(ctx context.Context) (context.Context, error) {
	return ctx, nil
}

func (h *cliTransHandler) OnInactive(ctx context.Context, conn net.Conn) {
	panic("unimplemented")
}

func (h *cliTransHandler) OnError(ctx context.Context, err error, conn net.Conn) {
	panic("unimplemented")
}

func (h *cliTransHandler) OnMessage(ctx context.Context, args, result remote.Message) (context.Context, error) {
	// do nothing
	return ctx, nil
}

func (h *cliTransHandler) SetPipeline(pipeline *remote.TransPipeline) {
}
