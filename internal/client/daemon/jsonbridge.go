package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

// EnvJSONPort pins the JSON bridge loopback port; 0 picks a free port.
const EnvJSONPort = "NIPA_DAEMON_JSON_PORT"

const (
	maxJSONBody       = maxRecvMsgSize
	jsonRoutePrefix   = "/v1/"
	jsonShutdownGrace = 2 * time.Second
	streamErrorGrace  = 250 * time.Millisecond
	mimeJSON          = "application/json"
	mimeNDJSON        = "application/x-ndjson"
)

var (
	jsonMarshal   = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}
	jsonUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}
)

type bridgeMethod struct {
	fullName string
	input    protoreflect.MessageDescriptor
	output   protoreflect.MessageDescriptor
	stream   bool
}

// jsonBridge exposes every NipaDaemon RPC as JSON over HTTP/1.1 for clients
// that cannot speak gRPC (Unity's Mono, Godot, Unreal). It is generic: the
// routes come from the service descriptor and calls go through a gRPC
// connection to the daemon's own listener, so the token interceptors stay the
// single auth point.
type jsonBridge struct {
	listener net.Listener
	conn     *grpc.ClientConn
	methods  map[string]bridgeMethod
	server   *http.Server
	cancel   context.CancelFunc
}

func newJSONBridge(listener net.Listener, grpcAddr string) (*jsonBridge, error) {
	conn, err := grpc.NewClient(grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)),
	)
	if err != nil {
		return nil, err
	}
	b := &jsonBridge{listener: listener, conn: conn, methods: bridgeMethods()}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.server = &http.Server{
		Handler:           b,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	return b, nil
}

func bridgeMethods() map[string]bridgeMethod {
	svc := daemonpb.File_internal_client_grpc_proto_daemon_proto.Services().ByName("NipaDaemon")
	methods := svc.Methods()
	out := make(map[string]bridgeMethod, methods.Len())
	for i := 0; i < methods.Len(); i++ {
		m := methods.Get(i)
		if m.IsStreamingClient() {
			continue
		}
		out[string(m.Name())] = bridgeMethod{
			fullName: fmt.Sprintf("/%s/%s", svc.FullName(), m.Name()),
			input:    m.Input(),
			output:   m.Output(),
			stream:   m.IsStreamingServer(),
		}
	}
	return out
}

func (b *jsonBridge) port() int {
	if addr, ok := b.listener.Addr().(*net.TCPAddr); ok {
		return addr.Port
	}
	return 0
}

func (b *jsonBridge) serve() error {
	err := b.server.Serve(b.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (b *jsonBridge) close() {
	b.cancel()
	ctx, stop := context.WithTimeout(context.Background(), jsonShutdownGrace)
	defer stop()
	if err := b.server.Shutdown(ctx); err != nil {
		_ = b.server.Close()
	}
	_ = b.conn.Close()
	_ = b.listener.Close()
}

func (b *jsonBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !loopbackHost(r.Host) || !loopbackOrigin(r.Header.Get("Origin")) {
		writeJSONError(w, http.StatusForbidden, codes.PermissionDenied, "non-loopback host or origin")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, codes.Unimplemented, "use POST")
		return
	}
	name, ok := strings.CutPrefix(r.URL.Path, jsonRoutePrefix)
	method, found := b.methods[name]
	if !ok || !found {
		writeJSONError(w, http.StatusNotFound, codes.Unimplemented, "unknown method "+r.URL.Path)
		return
	}
	in := dynamicpb.NewMessage(method.input)
	if err := readJSONBody(w, r, in); err != nil {
		writeJSONError(w, http.StatusBadRequest, codes.InvalidArgument, err.Error())
		return
	}
	ctx := r.Context()
	if token := r.Header.Get(TokenHeader); token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, TokenHeader, token)
	}
	if method.stream {
		b.stream(ctx, w, method, in)
		return
	}
	out := dynamicpb.NewMessage(method.output)
	if err := b.conn.Invoke(ctx, method.fullName, in, out); err != nil {
		writeGRPCError(w, err)
		return
	}
	data, err := jsonMarshal.Marshal(out)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, codes.Internal, err.Error())
		return
	}
	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(data)
}

type streamItem struct {
	msg *dynamicpb.Message
	err error
}

func (b *jsonBridge) stream(ctx context.Context, w http.ResponseWriter, method bridgeMethod, in *dynamicpb.Message) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := b.conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, method.fullName)
	if err == nil {
		err = stream.SendMsg(in)
	}
	if err == nil {
		err = stream.CloseSend()
	}
	if err == nil {
		_, err = stream.Header()
	}
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	items := make(chan streamItem)
	go func() {
		defer close(items)
		for {
			out := dynamicpb.NewMessage(method.output)
			err := stream.RecvMsg(out)
			select {
			case items <- streamItem{msg: out, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	// A rejected call is a trailers-only answer: its error arrives right
	// after the headers, so wait briefly for it before committing to 200.
	var first *streamItem
	select {
	case item := <-items:
		if item.err != nil && !errors.Is(item.err, io.EOF) {
			writeGRPCError(w, item.err)
			return
		}
		first = &item
	case <-time.After(streamErrorGrace):
	}

	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	w.Header().Set("Content-Type", mimeNDJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flush()
	emit := func(item streamItem) bool {
		if item.err != nil {
			if !errors.Is(item.err, io.EOF) && ctx.Err() == nil {
				writeStreamError(w, item.err)
				flush()
			}
			return false
		}
		data, err := jsonMarshal.Marshal(item.msg)
		var line bytes.Buffer
		if err != nil || json.Compact(&line, data) != nil {
			return false
		}
		line.WriteByte('\n')
		if _, err := w.Write(line.Bytes()); err != nil {
			return false
		}
		flush()
		return true
	}
	if first != nil && !emit(*first) {
		return
	}
	for item := range items {
		if !emit(item) {
			return
		}
	}
}

func readJSONBody(w http.ResponseWriter, r *http.Request, into *dynamicpb.Message) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := jsonUnmarshal.Unmarshal(data, into); err != nil {
		return fmt.Errorf("decode body: %w", err)
	}
	return nil
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return loopbackHost(u.Host)
}

type jsonError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSONError(w http.ResponseWriter, httpStatus int, code codes.Code, message string) {
	w.Header().Set("Content-Type", mimeJSON)
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(jsonError{Code: code.String(), Message: message})
}

func writeGRPCError(w http.ResponseWriter, err error) {
	st, _ := status.FromError(err)
	writeJSONError(w, httpStatusFor(st.Code()), st.Code(), st.Message())
}

func writeStreamError(w http.ResponseWriter, err error) {
	st, _ := status.FromError(err)
	_ = json.NewEncoder(w).Encode(map[string]jsonError{"error": {Code: st.Code().String(), Message: st.Message()}})
}

func httpStatusFor(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument, codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists, codes.Aborted, codes.FailedPrecondition:
		return http.StatusConflict
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Canceled:
		return 499
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}
