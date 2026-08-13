// FlameGate WASM Extension — OpenAI Codex
//go:build tinygo

package main

import (
	"encoding/json"
)

func main() {}

// --- Host imports ---
//go:wasmimport env http_post
func hostHTTPPost(urlPtr, urlLen, bodyPtr, bodyLen, hdrsPtr, hdrsLen uint32) uint32

//go:wasmimport env get_credentials
func hostGetCredentials(keyPtr, keyLen uint32) uint32

//go:wasmimport env emit_chunk
func hostEmitChunk(chunkPtr, chunkLen uint32)

//go:wasmimport env get_config_value
func hostGetConfigValue(keyPtr, keyLen uint32) uint32

var writeBuf [256 * 1024]byte
var writePos uint32

func memSlice(ptr, length uint32) []byte {
	if int(ptr)+int(length) > len(writeBuf) {
		return nil
	}
	return writeBuf[ptr : ptr+length]
}

func stringToPtr(s string) (uint32, uint32) {
	b := []byte(s)
	if writePos+uint32(len(b)) > uint32(len(writeBuf)) {
		return 0, 0
	}
	ptr := writePos
	copy(writeBuf[writePos:], b)
	writePos += uint32(len(b))
	return ptr, uint32(len(b))
}

//export alloc
func alloc(size uint32) uint32 {
	if size == 0 || writePos+size > uint32(len(writeBuf)) {
		return 0
	}
	ptr := writePos
	writePos += size
	return ptr
}

//export dealloc
func dealloc(ptr, size uint32) {}

// --- Types ---

type CanonicalReq struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Model       string   `json:"model"`
	Stream      bool     `json:"stream"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type ResponsesReq struct {
	Model      string         `json:"model"`
	Input      []ResponsesMsg `json:"input"`
	Stream     bool           `json:"stream"`
	MaxTokens  int            `json:"max_output_tokens,omitempty"`
}

type ResponsesMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

//export invoke
func invoke(reqPtr, reqLen uint32) uint32 {
	reqBytes := memSlice(reqPtr, reqLen)
	var req CanonicalReq
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		return writeJSON(`{"error":"invalid request"}`)
	}

	// Get credentials — Codex uses OAuth access_token
	ptr, size := stringToPtr("access_token")
	credPtr := hostGetCredentials(ptr, size)
	var token string
	if credPtr != 0 {
		s := memSlice(credPtr, 512)
		var creds map[string]string
		json.Unmarshal(s, &creds)
		token = creds["access_token"]
	}

	keyPtr, keyLen := stringToPtr("base_url")
	valPtr := hostGetConfigValue(keyPtr, keyLen)
	baseURL := "https://chatgpt.com/backend-api/codex"
	if valPtr != 0 {
		v := memSlice(valPtr, 256)
		baseURL = string(v)
	}

	resReq := ResponsesReq{
		Model:     req.Model,
		Stream:    req.Stream,
		MaxTokens: req.MaxTokens,
	}
	for _, m := range req.Messages {
		resReq.Input = append(resReq.Input, ResponsesMsg{Role: m.Role, Content: m.Content})
	}
	body, _ := json.Marshal(resReq)
	bodyPtr, bodyLen := stringToPtr(string(body))

	url := baseURL + "/responses"
	urlPtr, urlLen := stringToPtr(url)

	hdrs := `{"Content-Type":"application/json","Authorization":"Bearer ` + token + `","OpenAI-Beta":"responses=v1"}`
	hdrsPtr, hdrsLen := stringToPtr(hdrs)

	respPtr := hostHTTPPost(urlPtr, urlLen, bodyPtr, bodyLen, hdrsPtr, hdrsLen)
	if respPtr == 0 {
		return writeJSON(`{"error":"upstream failed"}`)
	}

	respBytes := memSlice(respPtr, 65536)

	if req.Stream {
		lines := splitLines(respBytes)
		for _, line := range lines {
			if !bytesStartsWith(line, []byte("data: ")) {
				continue
			}
			data := line[6:]
			if bytesEqual(data, []byte("[DONE]")) {
				break
			}
			var event map[string]interface{}
			if err := json.Unmarshal(data, &event); err != nil {
				continue
			}
			etype, _ := event["type"].(string)
			if etype == "response.output_text.delta" {
				delta, _ := event["delta"].(string)
				chunk, _ := json.Marshal(map[string]interface{}{
					"choices": []map[string]interface{}{
						{"delta": map[string]string{"content": delta}, "index": 0},
					},
				})
				emit(chunk)
			} else if etype == "response.completed" {
				chunk, _ := json.Marshal(map[string]interface{}{
					"choices": []map[string]interface{}{
						{"delta": map[string]string{"content": ""}, "index": 0, "finish_reason": "stop"},
					},
				})
				emit(chunk)
			}
		}
		return 0
	}

	var resp map[string]interface{}
	json.Unmarshal(respBytes, &resp)

	text := ""
	if output, ok := resp["output"].([]interface{}); ok {
		for _, item := range output {
			if m, ok := item.(map[string]interface{}); ok {
				if content, ok := m["content"].([]interface{}); ok {
					for _, c := range content {
						if cm, ok := c.(map[string]interface{}); ok {
							if cm["type"] == "output_text" {
								text, _ = cm["text"].(string)
							}
						}
					}
				}
			}
		}
	}

	model, _ := resp["model"].(string)
	canonical := map[string]interface{}{
		"object": "chat.completion",
		"model":  model,
		"choices": []map[string]interface{}{
			{"message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"},
		},
	}
	out, _ := json.Marshal(canonical)
	return writeJSON(string(out))
}

//export list_models
func list_models() uint32 {
	models := []Model{
		{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex"},
		{ID: "gpt-5.3-codex-high", Name: "GPT-5.3 Codex (High)"},
		{ID: "gpt-5.3-codex-low", Name: "GPT-5.3 Codex (Low)"},
		{ID: "gpt-5.2-codex", Name: "GPT-5.2 Codex"},
		{ID: "gpt-5.1-codex", Name: "GPT-5.1 Codex"},
		{ID: "gpt-5-codex", Name: "GPT-5 Codex"},
	}
	out, _ := json.Marshal(models)
	return writeJSON(string(out))
}

// --- helpers ---

func writeJSON(s string) uint32 {
	ptr := writePos
	b := []byte(s)
	if writePos+uint32(len(b)+4) > uint32(len(writeBuf)) {
		return 0
	}
	writeBuf[writePos] = byte(len(b))
	writeBuf[writePos+1] = byte(len(b) >> 8)
	writeBuf[writePos+2] = byte(len(b) >> 16)
	writeBuf[writePos+3] = byte(len(b) >> 24)
	writePos += 4
	copy(writeBuf[writePos:], b)
	writePos += uint32(len(b))
	return ptr
}

func emit(b []byte) {
	ptr, size := stringToPtr(string(b))
	if size > 0 {
		hostEmitChunk(ptr, size)
	}
}

func splitLines(b []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			lines = append(lines, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, b[start:])
	}
	return lines
}

func bytesStartsWith(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
