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
					Type     string `json:"type"`
					TargetID string `json:"targetId"`
				} `json:"targetInfo"`
			}
			json.Unmarshal(m.Params, &a)
			if a.TargetInfo.Type == "iframe" {
				b.frames[a.TargetInfo.TargetID] = a.SessionID
			}
		case "Target.detachedFromTarget":
			var d struct {
				SessionID string `json:"sessionId"`
				TargetID  string `json:"targetId"`
			}
			json.Unmarshal(m.Params, &d)
			if b.frames[d.TargetID] == d.SessionID {
				delete(b.frames, d.TargetID)
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
	p.Resize(width, height, false)
	b.must(nil, p.session, "Page.navigate", map[string]any{"url": url})
	return p
}

func (p *Page) Resize(width, height int, mobile bool) {
	p.t.Helper()
	p.b.must(nil, p.session, "Emulation.setDeviceMetricsOverride",
		map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": mobile})
	p.WaitFor(fmt.Sprintf("innerWidth === %d && innerHeight === %d", width, height))
	p.MustEval("(async () => { "+painted+" })()", nil)
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

func (p *Page) Screenshot() []byte {
	p.t.Helper()
	var shot struct {
		Data []byte `json:"data"`
	}
	p.b.must(&shot, p.session, "Page.captureScreenshot", map[string]any{"format": "png"})
	return shot.Data
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
		for (let last = ''; ; ) {
			el.scrollIntoView({block: 'center'});
			%s
			const r = el.getBoundingClientRect();
			const pt = {X: r.left + r.width / 2, Y: r.top + r.height / 2};
			const hit = document.elementFromPoint(pt.X, pt.Y);
			if (JSON.stringify(pt) === last && hit && el.contains(hit)) return pt;
			last = JSON.stringify(pt);
		}
	})()`, selector, painted), &pt)
	return pt
}

func (p *Page) ClickTimes(selector string, n int) {
	p.t.Helper()
	p.press(p.aim(selector), n)
}

func (p *Page) aim(selector string) point {
	p.t.Helper()
	deadline := time.Now().Add(callTimeout)
	for {
		pt := p.center(selector)
		p.MustEval(tracking, nil)
		p.hover(pt)
		var routed bool
		p.MustEval(reached(selector), &routed)
		if routed {
			return pt
		}
		p.repaint()
		if time.Now().After(deadline) {
			p.t.Fatalf("the pointer never reached %s", selector)
		}
	}
}

func reached(selector string) string {
	return fmt.Sprintf(`Boolean(window.cdptestHover && document.querySelector(%q).contains(cdptestHover))`, selector)
}

const tracking = `window.cdptestHover = null; if (!window.cdptestTracking) { window.cdptestTracking = true; addEventListener('pointermove', e => { cdptestHover = e.target; }, true); }`

func (p *Page) repaint() {
	p.t.Helper()
	p.MustEval(`(async () => {
		const frames = () => new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done)));
		document.documentElement.style.opacity = '0.999';
		await frames();
		document.documentElement.style.opacity = '';
		await frames();
	})()`, nil)
}

func (p *Page) hover(pt point) {
	p.t.Helper()
	for _, dx := range []float64{1, 0} {
		p.b.must(nil, p.session, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": pt.X + dx, "y": pt.Y})
	}
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
	session, err := f.session()
	if err != nil {
		return err
	}
	return f.p.b.eval(session, expr, into)
}

func (f *Frame) session() (string, error) {
	raw, err := f.p.b.call(f.p.session, "Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("document.querySelector(%q)", f.selector)})
	if err != nil {
		return "", err
	}
	var found struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &found); err != nil || found.Result.ObjectID == "" {
		return "", fmt.Errorf("no %s in the page", f.selector)
	}
	raw, err = f.p.b.call(f.p.session, "DOM.describeNode", map[string]any{"objectId": found.Result.ObjectID})
	f.p.b.call(f.p.session, "Runtime.releaseObject", map[string]any{"objectId": found.Result.ObjectID})
	if err != nil {
		return "", err
	}
	var owner struct {
		Node struct {
			FrameID string `json:"frameId"`
		} `json:"node"`
	}
	if err := json.Unmarshal(raw, &owner); err != nil {
		return "", err
	}
	f.p.b.mu.Lock()
	defer f.p.b.mu.Unlock()
	if session := f.p.b.frames[owner.Node.FrameID]; session != "" {
		return session, nil
	}
	return "", fmt.Errorf("%s has no attached document", f.selector)
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
	deadline := time.Now().Add(callTimeout)
	for last := (point{-1, -1}); ; {
		var inner struct {
			X, Y float64
			Hit  bool
		}
		f.MustEval(fmt.Sprintf(`(() => {
			const el = document.querySelector(%q);
			const r = el.getBoundingClientRect();
			const x = r.left + r.width / 2, y = r.top + r.height / 2;
			const hit = document.elementFromPoint(x, y);
			return {X: x, Y: y, Hit: Boolean(hit && el.contains(hit))};
		})()`, selector), &inner)
		var at point
		f.p.MustEval(fmt.Sprintf(`(async () => {
			const el = document.querySelector(%q);
			window.scrollBy({top: el.getBoundingClientRect().top + el.clientTop + %f - innerHeight / 2, behavior: 'instant'});
			%s
			const r = el.getBoundingClientRect();
			return {X: r.left + el.clientLeft, Y: r.top + el.clientTop};
		})()`, f.selector, inner.Y, painted), &at)
		at = point{at.X + inner.X, at.Y + inner.Y}
		if inner.Hit && at == last {
			f.MustEval(tracking, nil)
			f.p.hover(at)
			var routed bool
			f.MustEval(reached(selector), &routed)
			if routed {
				f.p.press(at, 1)
				return
			}
			f.p.repaint()
		}
		if time.Now().After(deadline) {
			f.p.t.Fatalf("%s in %s never settled under the pointer", selector, f.selector)
		}
		last = at
	}
}

type DragItem struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

func (p *Page) Drop(selector string, files []string, items ...DragItem) {
	p.t.Helper()
	pt := p.aim(selector)
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
