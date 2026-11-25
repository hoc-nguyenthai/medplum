// Lệnh trợ giúp cho tests/full_smoke (smoke.py chỉ dùng python3 chuẩn, không có thư viện DICOM hay websocket):
//
//	helper dicom <tệp ra>                  ghi một tệp DICOM tổng hợp (pkg/dicomtest), UID cố định theo smoke.py
//	helper ws <fhir-base> <access token>   Subscription websocket -> $get-ws-binding-token -> bind ở ws-gateway -> tạo Patient khớp -> chờ event-notification
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"

	"trivita-fhir/pkg/dicomtest"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "dùng: helper dicom <tệp ra> | helper ws <fhir-base> <token>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "dicom":
		b := dicomtest.Build(dicomtest.Meta{StudyUID: dicomtest.UID("9001"), SeriesUID: dicomtest.UID("9001", "1"), SOPUID: dicomtest.UID("9001", "1", "1"),
			Rows: 64, Cols: 64, Bits: 16, Frames: 1, PatientName: "Full^Smoke"})
		if err := os.WriteFile(os.Args[2], b, 0o600); err != nil {
			fail("ghi tệp DICOM: %v", err)
		}
	case "ws":
		if len(os.Args) < 4 {
			fail("thiếu token")
		}
		websocketSmoke(os.Args[2], os.Args[3])
	default:
		fail("lệnh lạ %q", os.Args[1])
	}
}

func fail(format string, a ...any) {
	fmt.Printf("FAIL "+format+"\n", a...)
	os.Exit(1)
}

func call(base, method, path, tok string, body any) (int, map[string]any) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, r)
	if err != nil {
		fail("%s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/fhir+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func websocketSmoke(base, tok string) {
	code, sub := call(base, "POST", "/Subscription", tok, map[string]any{
		"resourceType": "Subscription", "status": "active", "reason": "full smoke", "criteria": "Patient?family=WsSmoke",
		"channel": map[string]any{"type": "websocket"},
	})
	if code != http.StatusCreated {
		fail("tạo Subscription websocket %d", code)
	}
	id, _ := sub["id"].(string)
	code, p := call(base, "GET", "/Subscription/"+id+"/$get-ws-binding-token", tok, nil)
	if code != http.StatusOK {
		fail("$get-ws-binding-token %d", code)
	}
	var token, url string
	params, _ := p["parameter"].([]any)
	for _, x := range params {
		m, _ := x.(map[string]any)
		switch m["name"] {
		case "token":
			token, _ = m["valueString"].(string)
		case "websocket-url":
			url, _ = m["valueUrl"].(string)
		}
	}
	if token == "" || url == "" {
		fail("binding token thiếu token hoặc websocket-url: %v", p)
	}
	fmt.Println("OK   binding token, websocket-url " + url)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		fail("kết nối ws-gateway: %v", err)
	}
	defer c.CloseNow()
	bind, _ := json.Marshal(map[string]any{"type": "bind-with-token", "payload": map[string]string{"token": token}})
	if err := c.Write(ctx, websocket.MessageText, bind); err != nil {
		fail("gửi bind-with-token: %v", err)
	}
	handshake := false
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			fail("đọc websocket (handshake=%v): %v", handshake, err)
		}
		switch {
		case bytes.Contains(data, []byte(`"handshake"`)):
			handshake = true
			fmt.Println("OK   ws-gateway nhận bind (handshake)")
			if code, _ := call(base, "POST", "/Patient", tok, map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": "WsSmoke"}}}); code != http.StatusCreated {
				fail("tạo Patient khớp %d", code)
			}
		case bytes.Contains(data, []byte(`"event-notification"`)):
			fmt.Println("OK   websocket nhận event-notification (fhir-notifier khớp, ws-gateway giao)")
			return
		}
	}
}
