// Package source lets the app import an LX music source (a JavaScript file
// from the lx-music-source ecosystem) and use it as a fallback stream
// resolver for NetEase Cloud Music, and as a cross-platform search backend.
//
// Each source script expects a host object at globalThis.lx that speaks the
// LX contract: it registers a request handler with on(EVENT_NAMES.request,
// fn) and reports the platforms it serves with send(EVENT_NAMES.inited,
// {sources}). We run each source in its own goja runtime and its own
// goroutine, so a source's async work (await request(...), setTimeout) is
// driven by an event loop we own.
package source

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// hostUserAgent is what sources expect to be sending as.
const hostUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"

// lxBufKey marks the objects utils.buffer returns, so bufToString can read
// their bytes back.
const lxBufKey = "__lxBuffer"

// PlatformInfo is what a source reports it can do for one platform.
type PlatformInfo struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Actions  []string `json:"actions"`
	Qualitys []string `json:"qualitys"`
}

// Supports reports whether the platform serves action (musicUrl, search).
func (p PlatformInfo) Supports(action string) bool {
	for _, a := range p.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// SourceMeta is what a loaded source tells us about itself.
type SourceMeta struct {
	Name    string                  // @name from the header comment
	Author  string                  // @author
	Version string                  // @version
	Sources map[string]PlatformInfo // keyed by platform: wy, kw, kg, tx, mg
}

// Source is one imported music source, running in its own runtime and
// goroutine. Its methods may be called from other goroutines; the work
// crosses into the source goroutine through a channel.
type Source struct {
	rt      *goja.Runtime
	meta    SourceMeta
	handler bool // whether a request handler was registered

	reqCh   chan sourceReq
	closeCh chan struct{}
	closed  bool

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	timers  []*timer
	nextID  int
	pending map[int]chan sourceRes
}

type sourceReq struct {
	id     int
	action string
	source string
	info   map[string]any
}

type sourceRes struct {
	value any
	err   error
}

type timer struct {
	at time.Time
	cb goja.Callable
	id int
}

// runSource loads js in a fresh runtime and starts its event loop. It
// returns an error if the script fails to load or registers no request
// handler.
func runSource(js string, header SourceMeta) (*Source, error) {
	rt := goja.New()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Source{
		rt:      rt,
		meta:    header,
		reqCh:   make(chan sourceReq),
		closeCh: make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
		pending: map[int]chan sourceRes{},
	}
	s.injectGlobals()
	if _, err := rt.RunString(js); err != nil {
		cancel()
		return nil, fmt.Errorf("source: load script: %w", err)
	}
	if !s.handler {
		cancel()
		return nil, fmt.Errorf("source: no request handler registered (missing on(EVENT_NAMES.request))")
	}
	go s.loop()
	return s, nil
}

// injectGlobals wires globalThis.lx: the LX contract the sources expect.
func (s *Source) injectGlobals() {
	rt := s.rt
	lx := rt.NewObject()

	events := rt.NewObject()
	events.Set("request", "request")
	events.Set("inited", "inited")
	events.Set("updateAlert", "updateAlert")
	lx.Set("EVENT_NAMES", events)
	lx.Set("env", "desktop")
	lx.Set("version", "1.0.0")
	lx.Set("request", s.jsRequest)
	lx.Set("on", s.jsOn)
	lx.Set("send", s.jsSend)

	utils := rt.NewObject()
	buf := rt.NewObject()
	buf.Set("from", s.jsBufFrom)
	buf.Set("fromBase64", s.jsBufFromBase64)
	buf.Set("toBase64", s.jsBufToBase64)
	buf.Set("bufToString", s.jsBufToString)
	utils.Set("buffer", buf)
	crypto := rt.NewObject()
	crypto.Set("md5", s.jsMd5)
	crypto.Set("aesEncrypt", s.jsAesEncrypt)
	utils.Set("crypto", crypto)
	lx.Set("utils", utils)

	rt.GlobalObject().Set("lx", lx)

	// console: sources log liberally; keep it quiet but callable.
	console := rt.NewObject()
	log := func(goja.FunctionCall) goja.Value { return goja.Undefined() }
	for _, name := range []string{"log", "info", "warn", "error", "debug", "group", "groupEnd", "groupCollapsed"} {
		console.Set(name, log)
	}
	rt.Set("console", console)
	rt.Set("setTimeout", s.jsSetTimeout)
	rt.Set("clearTimeout", s.jsClearTimeout)
	rt.Set("__lxSettle", s.jsSettle)

	// The dispatch bridge: call the registered handler and settle its
	// promise back through __lxSettle, tagged with the request id.
	if _, err := rt.RunString(dispatchBridge); err != nil {
		panic(fmt.Sprintf("source: dispatch bridge: %v", err))
	}
	// btoa/atob, which goja lacks; sources use them for tokens.
	if _, err := rt.RunString(base64Polyfill); err != nil {
		panic(fmt.Sprintf("source: base64 polyfill: %v", err))
	}
}

// jsOn records the request handler.
func (s *Source) jsOn(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return goja.Undefined()
	}
	if call.Argument(0).String() != "request" {
		return goja.Undefined()
	}
	if fn, ok := goja.AssertFunction(call.Argument(1)); ok {
		// Keep the original JS function value: the bridge calls it as JS.
		s.rt.GlobalObject().Set("__lxHandler", call.Argument(1))
		s.handler = true
		_ = fn
	}
	return goja.Undefined()
}

// jsSend captures the inited report.
func (s *Source) jsSend(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return goja.Undefined()
	}
	if call.Argument(0).String() != "inited" {
		return goja.Undefined()
	}
	s.parseInited(call.Argument(1))
	return goja.Undefined()
}

// parseInited reads the sources map from send(EVENT_NAMES.inited, data).
func (s *Source) parseInited(data goja.Value) {
	obj, ok := data.(*goja.Object)
	if !ok {
		return
	}
	srcVal := obj.Get("sources")
	if srcVal == nil {
		return
	}
	exported, ok := srcVal.Export().(map[string]any)
	if !ok {
		return
	}
	sources := make(map[string]PlatformInfo, len(exported))
	for key, raw := range exported {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		info := PlatformInfo{}
		if v, ok := m["name"].(string); ok {
			info.Name = v
		}
		if v, ok := m["type"].(string); ok {
			info.Type = v
		}
		if v, ok := m["actions"].([]any); ok {
			for _, a := range v {
				if str, ok := a.(string); ok {
					info.Actions = append(info.Actions, str)
				}
			}
		}
		if v, ok := m["qualitys"].([]any); ok {
			for _, q := range v {
				if str, ok := q.(string); ok {
					info.Qualitys = append(info.Qualitys, str)
				}
			}
		}
		sources[key] = info
	}
	s.meta.Sources = sources
}

// jsRequest is the callback-style HTTP the sources call. It runs
// synchronously on the source goroutine, then invokes the JavaScript
// callback with (err, resp).
func (s *Source) jsRequest(call goja.FunctionCall) goja.Value {
	args := call.Arguments
	if len(args) < 1 {
		return goja.Undefined()
	}
	reqURL := args[0].String()
	var opts map[string]any
	var cb goja.Callable
	switch len(args) {
	case 2:
		if fn, ok := goja.AssertFunction(args[1]); ok {
			cb = fn
		} else if m, ok := args[1].Export().(map[string]any); ok {
			opts = m
		}
	case 3:
		if m, ok := args[1].Export().(map[string]any); ok {
			opts = m
		}
		if fn, ok := goja.AssertFunction(args[2]); ok {
			cb = fn
		}
	}
	status, body, err := s.doHTTP(reqURL, opts)
	var errVal goja.Value = goja.Null()
	if err != nil {
		errVal = s.rt.ToValue(err.Error())
	}
	resp := s.rt.NewObject()
	resp.Set("statusCode", status)
	if body != nil {
		resp.Set("body", s.rt.ToValue(body))
	} else {
		resp.Set("body", goja.Null())
	}
	if cb != nil {
		cb(s.rt.GlobalObject(), errVal, resp)
	}
	return goja.Undefined()
}

// doHTTP performs one request and returns the status and a body that is
// either a parsed JSON value or the raw string.
func (s *Source) doHTTP(reqURL string, opts map[string]any) (int, any, error) {
	method := "GET"
	if v, ok := opts["method"].(string); ok && v != "" {
		method = strings.ToUpper(v)
	}
	headers := map[string]string{}
	if h, ok := opts["headers"].(map[string]any); ok {
		for k, v := range h {
			headers[strings.ToLower(k)] = fmt.Sprint(v)
		}
	}
	var bodyReader io.Reader
	if b, ok := opts["body"]; ok && b != nil {
		switch bb := b.(type) {
		case string:
			bodyReader = strings.NewReader(bb)
		case map[string]any:
			if j, err := json.Marshal(bb); err == nil {
				bodyReader = bytes.NewReader(j)
				if headers["content-type"] == "" {
					headers["content-type"] = "application/json"
				}
			}
		}
	}
	if headers["user-agent"] == "" {
		headers["user-agent"] = hostUserAgent
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	var parsed any
	if json.Unmarshal(data, &parsed) == nil {
		return resp.StatusCode, parsed, nil
	}
	return resp.StatusCode, string(data), nil
}

// jsBufFrom returns an opaque buffer of the string's UTF-8 bytes.
func (s *Source) jsBufFrom(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return goja.Undefined()
	}
	return s.rt.ToValue(map[string]any{lxBufKey: base64.StdEncoding.EncodeToString([]byte(call.Argument(0).String()))})
}

// jsBufFromBase64 decodes a base64 string into a buffer.
func (s *Source) jsBufFromBase64(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return goja.Undefined()
	}
	b, err := base64.StdEncoding.DecodeString(call.Argument(0).String())
	if err != nil {
		return goja.Undefined()
	}
	return s.rt.ToValue(map[string]any{lxBufKey: base64.StdEncoding.EncodeToString(b)})
}

// jsBufToBase64 encodes a buffer to base64.
func (s *Source) jsBufToBase64(call goja.FunctionCall) goja.Value {
	return s.rt.ToValue(base64.StdEncoding.EncodeToString(bufBytes(call.Argument(0))))
}

// jsBufToString converts a buffer to a string, optionally base64.
func (s *Source) jsBufToString(call goja.FunctionCall) goja.Value {
	b := bufBytes(call.Argument(0))
	enc := ""
	if len(call.Arguments) > 1 {
		enc = call.Argument(1).String()
	}
	if enc == "base64" {
		return s.rt.ToValue(base64.StdEncoding.EncodeToString(b))
	}
	return s.rt.ToValue(string(b))
}

func bufBytes(v goja.Value) []byte {
	if v == nil {
		return nil
	}
	obj, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	marker := obj.Get(lxBufKey)
	if marker == nil {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(marker.String())
	if err != nil {
		return nil
	}
	return b
}

// jsMd5 returns the hex MD5 of the argument.
func (s *Source) jsMd5(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return goja.Undefined()
	}
	sum := md5.Sum([]byte(call.Argument(0).String()))
	return s.rt.ToValue(hex.EncodeToString(sum[:]))
}

// jsAesEncrypt is a best-effort port of lx's utils.crypto.aesEncrypt:
// aesEncrypt(data, key, iv, mode) -> base64, supporting ecb and cbc.
func (s *Source) jsAesEncrypt(call goja.FunctionCall) goja.Value {
	args := call.Arguments
	if len(args) < 3 {
		return goja.Undefined()
	}
	data := []byte(args[0].String())
	key := []byte(args[1].String())
	iv := []byte(args[2].String())
	mode := "cbc"
	if len(args) > 3 {
		mode = args[3].String()
	}
	out, err := aesEncrypt(data, key, iv, mode)
	if err != nil {
		return goja.Undefined()
	}
	return s.rt.ToValue(base64.StdEncoding.EncodeToString(out))
}

// jsSetTimeout schedules cb after delay milliseconds, on the source
// goroutine. It returns an id clearTimeout can cancel.
func (s *Source) jsSetTimeout(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return goja.Undefined()
	}
	cb, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		return goja.Undefined()
	}
	delay := 0.0
	if len(call.Arguments) > 1 {
		delay = call.Argument(1).ToFloat()
	}
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.timers = append(s.timers, &timer{at: time.Now().Add(time.Duration(delay) * time.Millisecond), cb: cb, id: id})
	s.mu.Unlock()
	return s.rt.ToValue(id)
}

// jsClearTimeout cancels a scheduled timer by id.
func (s *Source) jsClearTimeout(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return goja.Undefined()
	}
	id := int(call.Argument(0).ToInteger())
	s.mu.Lock()
	remain := s.timers[:0]
	for _, t := range s.timers {
		if t.id != id {
			remain = append(remain, t)
		}
	}
	s.timers = remain
	s.mu.Unlock()
	return goja.Undefined()
}

// jsSettle is called from the dispatch bridge when a handler's promise
// settles, tagged with the request id.
func (s *Source) jsSettle(call goja.FunctionCall) goja.Value {
	id := int(call.Argument(0).ToInteger())
	ok := call.Argument(1).ToBoolean()
	val := call.Argument(2)
	var res sourceRes
	if ok {
		if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
			res.value = val.Export()
		}
	} else {
		msg := ""
		if val != nil && !goja.IsUndefined(val) {
			msg = val.String()
		}
		res.err = errors.New(msg)
	}
	s.settle(id, res)
	return goja.Undefined()
}

// settle hands a result to the request waiting on id.
func (s *Source) settle(id int, res sourceRes) {
	s.mu.Lock()
	ch := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ch != nil {
		ch <- res
	}
}

// loop is the source's event loop: it dispatches requests and fires
// timers, draining microtasks after each so Promise callbacks run.
func (s *Source) loop() {
	for {
		s.mu.Lock()
		now := time.Now()
		nearest := time.Hour
		hasTimer := false
		for _, t := range s.timers {
			if d := t.at.Sub(now); d < nearest {
				nearest = d
				hasTimer = true
			}
		}
		s.mu.Unlock()

		var timerCh <-chan time.Time
		if hasTimer && nearest > 0 {
			timerCh = time.After(nearest)
		}
		select {
		case <-s.closeCh:
			return
		case req := <-s.reqCh:
			s.handle(req)
			s.drain()
		case <-timerCh:
			s.fireTimers()
			s.drain()
		}
	}
}

// handle dispatches one request through the JS bridge.
func (s *Source) handle(req sourceReq) {
	infoJSON, err := json.Marshal(req.info)
	if err != nil {
		s.settle(req.id, sourceRes{err: err})
		return
	}
	call := fmt.Sprintf("__lxDispatch(%d, %q, %q, %s)", req.id, req.action, req.source, infoJSON)
	if _, err := s.rt.RunString(call); err != nil {
		s.settle(req.id, sourceRes{err: err})
	}
}

// fireTimers runs every timer due now, which may resolve Promises that
// the next drain turns into results.
func (s *Source) fireTimers() {
	now := time.Now()
	s.mu.Lock()
	var remain, due []*timer
	for _, t := range s.timers {
		if !t.at.After(now) {
			due = append(due, t)
		} else {
			remain = append(remain, t)
		}
	}
	s.timers = remain
	s.mu.Unlock()
	for _, t := range due {
		t.cb(s.rt.GlobalObject())
	}
}

// drain runs the microtask queue so Promise then-callbacks (and the
// results they post) flush.
func (s *Source) drain() {
	_, _ = s.rt.RunString("0")
}

// Request sends action to the source and waits for the result. It may be
// called from any goroutine.
func (s *Source) Request(action, source string, info map[string]any) (any, error) {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	ch := make(chan sourceRes, 1)
	s.pending[id] = ch
	s.mu.Unlock()

	select {
	case s.reqCh <- sourceReq{id: id, action: action, source: source, info: info}:
	case <-s.closeCh:
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, errors.New("source closed")
	}
	res := <-ch
	if res.err != nil {
		return nil, res.err
	}
	return res.value, nil
}

// Meta returns what the source reported about itself.
func (s *Source) Meta() SourceMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta
}

// Close stops the source's event loop.
func (s *Source) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.closeCh)
	s.cancel()
}

// dispatchBridge calls the registered handler and settles its promise back
// through __lxSettle, tagged with the request id.
const dispatchBridge = `
this.__lxDispatch = function(id, action, source, info) {
  try {
    var r = __lxHandler({ action: action, source: source, info: info });
    Promise.resolve(r).then(
      function(v) { __lxSettle(id, true, v); },
      function(e) { __lxSettle(id, false, (e && e.message) ? e.message : String(e)); }
    );
  } catch (e) {
    __lxSettle(id, false, (e && e.message) ? e.message : String(e));
  }
};
`

// base64Polyfill provides btoa/atob over Latin1 strings, as browsers do.
const base64Polyfill = `
(function() {
  var B64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  this.btoa = function(s) {
    s = String(s);
    var out = '', i = 0;
    while (i < s.length) {
      var c1 = s.charCodeAt(i++), c2 = i < s.length ? s.charCodeAt(i++) : NaN, c3 = i < s.length ? s.charCodeAt(i++) : NaN;
      var e1 = c1 >> 2, e2 = ((c1 & 3) << 4) | (isNaN(c2) ? 0 : c2 >> 4);
      var e3 = isNaN(c2) ? 64 : (((c2 & 15) << 2) | (isNaN(c3) ? 0 : c3 >> 6));
      var e4 = isNaN(c3) ? 64 : (c3 & 63);
      out += B64.charAt(e1) + B64.charAt(e2) + B64.charAt(e3) + B64.charAt(e4);
    }
    return out;
  };
  this.atob = function(s) {
    s = String(s);
    var out = '', i = 0;
    while (i < s.length) {
      var e1 = B64.indexOf(s.charAt(i++)), e2 = B64.indexOf(s.charAt(i++));
      var e3 = B64.indexOf(s.charAt(i++)), e4 = B64.indexOf(s.charAt(i++));
      var c1 = (e1 << 2) | (e2 >> 4);
      var c2 = ((e2 & 15) << 4) | (e3 >> 2);
      var c3 = ((e3 & 3) << 6) | e4;
      out += String.fromCharCode(c1);
      if (e3 !== 64) out += String.fromCharCode(c2);
      if (e4 !== 64) out += String.fromCharCode(c3);
    }
    return out;
  };
}).call(this);
`
