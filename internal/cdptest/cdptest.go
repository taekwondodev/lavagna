package cdptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

const callTimeout = 10 * time.Second

type Browser struct {
	t       testing.TB
	w       *os.File
	mu      sync.Mutex
	next    int
	pending map[int]chan message
	paused  map[string][]string
	frames  map[string]string
}

type message struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     json.RawMessage `json:"error,omitempty"`
}

type request struct {
	ID        int    `json:"id"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

func chromePath() string {
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func Start(t testing.TB) *Browser {
	path := chromePath()
	if path == "" {
		t.Skip("no Chrome or Chromium installed")
	}
	toChromeR, toChromeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fromChromeR, fromChromeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "--headless=new", "--remote-debugging-pipe", "--use-mock-keychain", "--password-store=basic",
		"--user-data-dir="+t.TempDir(), "--no-first-run", "--no-default-browser-check", "about:blank")
	cmd.ExtraFiles = []*os.File{toChromeR, fromChromeW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	toChromeR.Close()
	fromChromeW.Close()
	t.Cleanup(func() {
		toChromeW.Close()
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
	})
	b := &Browser{t: t, w: toChromeW, pending: map[int]chan message{}, paused: map[string][]string{}, frames: map[string]string{}}
	go b.read(fromChromeR)
	return b
}

func (b *Browser) read(r *os.File) {
	rd := bufio.NewReader(r)
	for {
		raw, err := rd.ReadBytes(0)
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(raw[:len(raw)-1], &m) != nil {
			continue
		}
		b.mu.Lock()
		switch m.Method {
		case "Fetch.requestPaused":
			var p struct {
				RequestID string `json:"requestId"`
			}
			json.Unmarshal(m.Params, &p)
			b.paused[m.SessionID] = append(b.paused[m.SessionID], p.RequestID)
		case "Target.attachedToTarget":
			var a struct {
				SessionID  string `json:"sessionId"`
				TargetInfo struct {
					Type string `json:"type"`
				} `json:"targetInfo"`
			}
			json.Unmarshal(m.Params, &a)
			if a.TargetInfo.Type == "iframe" {
				b.frames[m.SessionID] = a.SessionID
			}
		case "Target.detachedFromTarget":
			var d struct {
				SessionID string `json:"sessionId"`
			}
			json.Unmarshal(m.Params, &d)
			if b.frames[m.SessionID] == d.SessionID {
				delete(b.frames, m.SessionID)
			}
		}
		ch := b.pending[m.ID]
		delete(b.pending, m.ID)
		b.mu.Unlock()
		if m.ID != 0 && ch != nil {
			ch <- m
		}
	}
}

func (b *Browser) call(session, method string, params any) (json.RawMessage, error) {
	b.mu.Lock()
	b.next++
	id := b.next
	ch := make(chan message, 1)
	b.pending[id] = ch
	b.mu.Unlock()
	raw, err := json.Marshal(request{ID: id, Method: method, Params: params, SessionID: session})
	if err != nil {
		return nil, err
	}
	if _, err := b.w.Write(append(raw, 0)); err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, m.Error)
		}
		return m.Result, nil
	case <-time.After(callTimeout):
		return nil, fmt.Errorf("%s: timeout", method)
	}
}

func (b *Browser) must(into any, session, method string, params any) {
	b.t.Helper()
	raw, err := b.call(session, method, params)
	if err != nil {
		b.t.Fatal(err)
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			b.t.Fatalf("%s: %v", method, err)
		}
	}
}

type Page struct {
	t       testing.TB
	b       *Browser
	target  string
	session string
}

func (b *Browser) Open(url string, width, height int) *Page {
	b.t.Helper()
	var target struct {
		TargetID string `json:"targetId"`
	}
	b.must(&target, "", "Target.createTarget", map[string]any{"url": "about:blank"})
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	b.must(&attached, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true})
	p := &Page{t: b.t, b: b, target: target.TargetID, session: attached.SessionID}
	b.must(nil, p.session, "Target.setAutoAttach",
		map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true})
	b.must(nil, p.session, "Emulation.setDeviceMetricsOverride",
		map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false})
	b.must(nil, p.session, "Page.navigate", map[string]any{"url": url})
	return p
}

func (p *Page) Close() {
	p.t.Helper()
	p.b.must(nil, "", "Target.closeTarget", map[string]any{"targetId": p.target})
	deadline := time.Now().Add(callTimeout)
	for {
		var targets struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
			} `json:"targetInfos"`
		}
		p.b.must(&targets, "", "Target.getTargets", map[string]any{})
		open := false
		for _, info := range targets.TargetInfos {
			open = open || info.TargetID == p.target
		}
		if !open {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("target %s did not close", p.target)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (p *Page) Reload() {
	p.t.Helper()
	p.b.must(nil, p.session, "Page.reload", map[string]any{})
}

func (p *Page) eval(expr string, into any) error { return p.b.eval(p.session, expr, into) }

func (b *Browser) eval(session, expr string, into any) error {
	raw, err := b.call(session, "Runtime.evaluate",
		map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true})
	if err != nil {
		return err
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return fmt.Errorf("%s: %s %s", expr, r.ExceptionDetails.Text, r.ExceptionDetails.Exception.Description)
	}
	if into == nil || r.Result.Value == nil {
		return nil
	}
	return json.Unmarshal(r.Result.Value, into)
}

func (p *Page) MustEval(expr string, into any) {
	p.t.Helper()
	if err := p.eval(expr, into); err != nil {
		p.t.Fatal(err)
	}
}

func (p *Page) WaitFor(expr string) {
	p.t.Helper()
	waitFor(p.t, p.eval, expr)
}

func waitFor(t testing.TB, eval func(string, any) error, expr string) {
	t.Helper()
	deadline := time.Now().Add(callTimeout)
	for {
		var ok bool
		if eval("Boolean("+expr+")", &ok) == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", expr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (p *Page) Click(selector string) {
	p.t.Helper()
	p.ClickTimes(selector, 1)
}

func (p *Page) center(selector string) (pt point) {
	p.t.Helper()
	p.b.must(nil, p.session, "Page.bringToFront", map[string]any{})
	p.MustEval(fmt.Sprintf(`(async () => {
		const el = document.querySelector(%q);
		el.scrollIntoView({block: 'center'});
		%s
		const r = el.getBoundingClientRect();
		return {X: r.left + r.width / 2, Y: r.top + r.height / 2};
	})()`, selector, painted), &pt)
	return pt
}

func (p *Page) ClickTimes(selector string, n int) {
	p.t.Helper()
	p.press(p.center(selector), n)
}

type point struct{ X, Y float64 }

const painted = `await new Promise(done => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(done, 50))));`

func (p *Page) press(pt point, n int) {
	p.t.Helper()
	for i := 0; i < n; i++ {
		for _, typ := range []string{"mousePressed", "mouseReleased"} {
			p.b.must(nil, p.session, "Input.dispatchMouseEvent",
				map[string]any{"type": typ, "x": pt.X, "y": pt.Y, "button": "left", "clickCount": 1})
		}
	}
}

func (p *Page) Type(selector, text string) {
	p.t.Helper()
	p.Click(selector)
	p.Insert(text)
}

func (p *Page) Insert(text string) {
	p.t.Helper()
	p.b.must(nil, p.session, "Input.insertText", map[string]any{"text": text})
}

func (p *Page) SelectAll() {
	p.t.Helper()
	for _, typ := range []string{"rawKeyDown", "keyUp"} {
		p.b.must(nil, p.session, "Input.dispatchKeyEvent",
			map[string]any{"type": typ, "key": "a", "code": "KeyA", "windowsVirtualKeyCode": 65, "modifiers": 4, "commands": []string{"selectAll"}})
	}
}

func (p *Page) Hold(urlPattern string) {
	p.t.Helper()
	p.b.must(nil, p.session, "Fetch.enable", map[string]any{"patterns": []map[string]string{{"urlPattern": urlPattern}}})
}

func (p *Page) Held() []string {
	p.b.mu.Lock()
	defer p.b.mu.Unlock()
	return append([]string(nil), p.b.paused[p.session]...)
}

func (p *Page) Release(id string) {
	p.t.Helper()
	p.b.must(nil, p.session, "Fetch.continueRequest", map[string]any{"requestId": id})
}

func (p *Page) Press(key string, keyCode, modifiers int) {
	p.t.Helper()
	for _, typ := range []string{"rawKeyDown", "keyUp"} {
		p.b.must(nil, p.session, "Input.dispatchKeyEvent",
			map[string]any{"type": typ, "key": key, "code": key, "windowsVirtualKeyCode": keyCode, "modifiers": modifiers})
	}
}

func (p *Page) Wheel(deltaY float64) {
	p.t.Helper()
	p.b.must(nil, p.session, "Input.dispatchMouseEvent",
		map[string]any{"type": "mouseWheel", "x": 200, "y": 400, "deltaX": 0, "deltaY": deltaY})
}

type Frame struct {
	p        *Page
	selector string
}

func (p *Page) Frame(selector string) *Frame { return &Frame{p: p, selector: selector} }

func (f *Frame) eval(expr string, into any) error {
	f.p.b.mu.Lock()
	session := f.p.b.frames[f.p.session]
	f.p.b.mu.Unlock()
	if session == "" {
		return fmt.Errorf("no frame attached to the page")
	}
	return f.p.b.eval(session, expr, into)
}

func (f *Frame) MustEval(expr string, into any) {
	f.p.t.Helper()
	waitFor(f.p.t, f.eval, "document.readyState === 'complete'")
	if err := f.eval(expr, into); err != nil {
		f.p.t.Fatal(err)
	}
}

func (f *Frame) WaitFor(expr string) {
	f.p.t.Helper()
	waitFor(f.p.t, f.eval, expr)
}

func (f *Frame) Click(selector string) {
	f.p.t.Helper()
	f.p.b.must(nil, f.p.session, "Page.bringToFront", map[string]any{})
	var r struct{ X, Y, Width, Height float64 }
	f.MustEval(fmt.Sprintf(`(() => {
		const r = document.querySelector(%q).getBoundingClientRect();
		return {X: r.left, Y: r.top, Width: r.width, Height: r.height};
	})()`, selector), &r)
	box := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		const r = el.getBoundingClientRect();
		return {X: r.left + el.clientLeft, Y: r.top + el.clientTop};
	})()`, f.selector)
	var at point
	f.p.MustEval(box, &at)
	f.p.MustEval(fmt.Sprintf(`(async () => { window.scrollBy({top: %f - innerHeight / 2, behavior: 'instant'}); %s })()`, at.Y+r.Y+r.Height/2, painted), nil)
	f.p.MustEval(box, &at)
	f.p.press(point{at.X + r.X + r.Width/2, at.Y + r.Y + r.Height/2}, 1)
}

type DragItem struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

func (p *Page) Drop(selector string, files []string, items ...DragItem) {
	p.t.Helper()
	pt := p.center(selector)
	data := map[string]any{"items": append([]DragItem{}, items...), "files": append([]string{}, files...), "dragOperationsMask": 1}
	for _, typ := range []string{"dragEnter", "dragOver", "drop"} {
		p.b.must(nil, p.session, "Input.dispatchDragEvent", map[string]any{"type": typ, "x": pt.X, "y": pt.Y, "data": data})
	}
}

func (p *Page) Paste(mimeType string, data []byte) {
	p.t.Helper()
	p.b.must(nil, "", "Browser.grantPermissions", map[string]any{"permissions": []string{"clipboardReadWrite", "clipboardSanitizedWrite"}})
	p.b.must(nil, p.session, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true})
	encoded, _ := json.Marshal(data)
	p.MustEval(fmt.Sprintf(`(async () => {
		const bytes = Uint8Array.from(atob(%s), c => c.charCodeAt(0));
		await navigator.clipboard.write([new ClipboardItem({%q: new Blob([bytes], {type: %q})})]);
	})()`, encoded, mimeType, mimeType), nil)
	for _, typ := range []string{"rawKeyDown", "keyUp"} {
		p.b.must(nil, p.session, "Input.dispatchKeyEvent",
			map[string]any{"type": typ, "key": "v", "code": "KeyV", "windowsVirtualKeyCode": 86, "modifiers": 4, "commands": []string{"paste"}})
	}
}
