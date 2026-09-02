// FlameGate WASM Extension — Cline
//go:build tinygo

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unsafe"
)

func main() {}

// --- Host imports ---
//
//go:wasmimport env http_post
func hostHTTPPost(urlPtr, urlLen, bodyPtr, bodyLen, hdrsPtr, hdrsLen uint32) uint32

//go:wasmimport env http_get
func hostHTTPGet(urlPtr, urlLen, hdrsPtr, hdrsLen uint32) uint32

//go:wasmimport env get_credentials
func hostGetCredentials(keyPtr, keyLen uint32) uint32

//go:wasmimport env emit_chunk
func hostEmitChunk(chunkPtr, chunkLen uint32)

//go:wasmimport env fg_log
func hostLog(levelPtr, levelLen, msgPtr, msgLen uint32)

var writeBuf [512 * 1024]byte
var writePos uint32

// bufBase returns the absolute WASM linear-memory address of writeBuf.
// Every pointer handed to the host must be bufBase()+offset (wazero writes at
// absolute linear-memory offsets; a raw buffer offset of 0 is mistaken for an
// allocation failure).
func bufBase() uint32 {
	return uint32(uintptr(unsafe.Pointer(&writeBuf[0])))
}

func toOffset(ptr uint32) uint32 {
	return ptr - bufBase()
}

func memSlice(ptr, length uint32) []byte {
	off := toOffset(ptr)
	if off+length > uint32(len(writeBuf)) {
		return nil
	}
	return writeBuf[off : off+length]
}

func stringToPtr(s string) (uint32, uint32) {
	b := []byte(s)
	if writePos+uint32(len(b)) > uint32(len(writeBuf)) {
		return 0, 0
	}
	ptr := writePos
	copy(writeBuf[writePos:], b)
	writePos += uint32(len(b))
	return bufBase() + ptr, uint32(len(b))
}

//export alloc
func alloc(size uint32) uint32 {
	if size == 0 || writePos+size > uint32(len(writeBuf)) {
		return 0
	}
	ptr := writePos
	writePos += size
	return bufBase() + ptr
}

//export dealloc
func dealloc(ptr, size uint32) {}

// --- Host protocol helpers ---

// readLenPrefixed reads a [4-byte LE length][payload] blob the host wrote at
// the absolute address ptr (writeGuestJSON / writeGuestRawLenPrefix shape).
func readLenPrefixed(ptr uint32) []byte {
	if ptr == 0 {
		return nil
	}
	lb := memSlice(ptr, 4)
	if lb == nil {
		return nil
	}
	n := uint32(lb[0]) | uint32(lb[1])<<8 | uint32(lb[2])<<16 | uint32(lb[3])<<24
	if n == 0 || n > 512*1024 {
		return nil
	}
	return memSlice(ptr+4, n)
}

// getCredentials calls get_credentials and returns api_key/base_url.
func getCredentials() map[string]string {
	kPtr, kLen := stringToPtr("default")
	credPtr := hostGetCredentials(kPtr, kLen)
	raw := readLenPrefixed(credPtr)
	creds := map[string]string{}
	if raw == nil {
		return creds
	}
	creds["api_key"] = extractJSONString(raw, "api_key")
	creds["base_url"] = extractJSONString(raw, "base_url")
	return creds
}

// writeJSON writes [4-byte LE len][json] at the current write position and
// returns the absolute address of the length prefix (host readGuestJSON shape).
func writeJSON(s string) uint32 {
	pos := writePos
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
	return bufBase() + pos
}

func emit(b []byte) {
	ptr, size := stringToPtr(string(b))
	if size > 0 {
		hostEmitChunk(ptr, size)
	}
}

func logf(level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	lPtr, lLen := stringToPtr(level)
	mPtr, mLen := stringToPtr(msg)
	hostLog(lPtr, lLen, mPtr, mLen)
}

// --- TinyGo wasm-unknown JSON note ---
// json.Marshal works; json.Unmarshal / json.Decoder misbehave (reflection
// against destination pointers fails nondeterministically). All parsing below
// is manual and dependency-free.

// extractJSONString returns the value of the first `"key":"..."` occurrence
// (raw, unescaped handling kept minimal: strips quotes, no escape expansion).
func extractJSONString(data []byte, key string) string {
	needle := []byte(`"` + key + `"`)
	idx := indexBytes(data, needle)
	if idx < 0 {
		return ""
	}
	rest := data[idx+len(needle):]
	// skip whitespace and ':'
	i := 0
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\n' || rest[i] == '\r') {
		i++
	}
	if i >= len(rest) || rest[i] != ':' {
		return ""
	}
	i++
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\n' || rest[i] == '\r') {
		i++
	}
	if i >= len(rest) || rest[i] != '"' {
		return ""
	}
	i++
	start := i
	for i < len(rest) && rest[i] != '"' {
		if rest[i] == '\\' {
			i += 2 // skip escaped char (keeps it raw, fine for tokens)
			continue
		}
		i++
	}
	return string(rest[start:i])
}

// extractJSONBool returns the value of `"key":true|false`.
func extractJSONBool(data []byte, key string) bool {
	needle := []byte(`"` + key + `"`)
	idx := indexBytes(data, needle)
	if idx < 0 {
		return false
	}
	rest := data[idx+len(needle):]
	i := 0
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\n' || rest[i] == '\r') {
		i++
	}
	if i >= len(rest) || rest[i] != ':' {
		return false
	}
	i++
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\n' || rest[i] == '\r') {
		i++
	}
	return i+4 <= len(rest) && rest[i] == 't' && rest[i+1] == 'r' && rest[i+2] == 'u' && rest[i+3] == 'e'
}

// extractJSONNestedString returns `"outer":{"inner":"value"}` — used for
// `{"delta":{"content":"..."}}` and `{"error":{"message":"..."}}`.
func extractJSONNestedString(data []byte, outer, inner string) string {
	outerNeedle := []byte(`"` + outer + `"`)
	idx := indexBytes(data, outerNeedle)
	if idx < 0 {
		return ""
	}
	rest := data[idx+len(outerNeedle):]
	// find '{' that starts the nested object
	i := 0
	for i < len(rest) && rest[i] != '{' {
		i++
	}
	if i >= len(rest) {
		return ""
	}
	// search within the nested object only (bounded by matching brace)
	obj := rest[i:]
	depth := 0
	end := 0
	for j := 0; j < len(obj); j++ {
		if obj[j] == '{' {
			depth++
		} else if obj[j] == '}' {
			depth--
			if depth == 0 {
				end = j
				break
			}
		}
	}
	if end == 0 {
		end = len(obj)
	}
	return extractJSONString(obj[:end], inner)
}

// extractMessageContent handles both string content ("content":"hello") and
// multimodal ContentPart arrays ("content":[{"type":"text","text":"hello"}])
// serialized by FlameGate's domain model.
func extractMessageContent(obj []byte) string {
	if s := extractJSONString(obj, "content"); s != "" {
		return s
	}

	needle := []byte(`"content"`)
	idx := indexBytes(obj, needle)
	if idx < 0 {
		return ""
	}
	rest := obj[idx+len(needle):]
	i := 0
	for i < len(rest) && (rest[i] == ' ' || rest[i] == ':') {
		i++
	}
	if i >= len(rest) || rest[i] != '[' {
		return ""
	}

	arr := rest[i:]
	var textParts stringsBuilder
	k := 0
	for {
		tIdx := indexBytes(arr[k:], []byte(`"text"`))
		if tIdx < 0 {
			break
		}
		pos := k + tIdx + len(`"text"`)
		for pos < len(arr) && (arr[pos] == ' ' || arr[pos] == ':') {
			pos++
		}
		if pos < len(arr) && arr[pos] == '"' {
			vStart := pos + 1
			vEnd := vStart
			for vEnd < len(arr) && arr[vEnd] != '"' {
				if arr[vEnd] == '\\' {
					vEnd += 2
					continue
				}
				vEnd++
			}
			if vEnd <= len(arr) {
				textParts.write(string(arr[vStart:vEnd]))
			}
			k = vEnd + 1
		} else {
			k = pos + 1
		}
	}
	return textParts.string()
}

// extractMessages parses `"messages":[{"role":"r","content":"c"},...]` into a
// []CanonicalMessage.
func extractMessages(data []byte) []CanonicalMessage {
	needle := []byte(`"messages"`)
	idx := indexBytes(data, needle)
	if idx < 0 {
		return nil
	}
	rest := data[idx+len(needle):]
	i := 0
	for i < len(rest) && rest[i] != '[' {
		i++
	}
	if i >= len(rest) {
		return nil
	}
	// scan array items; each is an object
	arr := rest[i:]
	var out []CanonicalMessage
	j := 0
	for j < len(arr) {
		if arr[j] == '{' {
			depth := 0
			end := j
			for k := j; k < len(arr); k++ {
				if arr[k] == '{' {
					depth++
				} else if arr[k] == '}' {
					depth--
					if depth == 0 {
						end = k
						break
					}
				}
			}
			obj := arr[j : end+1]
			out = append(out, CanonicalMessage{
				Role:    extractJSONString(obj, "role"),
				Content: extractMessageContent(obj),
			})
			j = end + 1
		} else {
			j++
		}
	}
	return out
}

// extractStringMap parses `"headers":{...}` into map[string]string.
func extractStringMap(data []byte, key string) map[string]string {
	needle := []byte(`"` + key + `"`)
	idx := indexBytes(data, needle)
	if idx < 0 {
		return nil
	}
	rest := data[idx+len(needle):]
	i := 0
	for i < len(rest) && rest[i] != '{' {
		i++
	}
	if i >= len(rest) {
		return nil
	}
	obj := rest[i:]
	depth := 0
	end := 0
	for j := 0; j < len(obj); j++ {
		if obj[j] == '{' {
			depth++
		} else if obj[j] == '}' {
			depth--
			if depth == 0 {
				end = j
				break
			}
		}
	}
	if end == 0 {
		end = len(obj)
	}
	inner := obj[:end]
	out := map[string]string{}
	// walk "k":"v" pairs
	k := 0
	for {
		start := indexBytes(inner[k:], []byte(`"`))
		if start < 0 {
			break
		}
		pos := k + start + 1
		keyEnd := pos
		for keyEnd < len(inner) && inner[keyEnd] != '"' {
			keyEnd++
		}
		keyName := string(inner[pos:keyEnd])
		colon := indexBytes(inner[keyEnd:], []byte(`:`))
		if colon < 0 {
			break
		}
		vStart := keyEnd + colon + 1
		for vStart < len(inner) && inner[vStart] != '"' {
			vStart++
		}
		if vStart >= len(inner) {
			break
		}
		vEnd := vStart + 1
		for vEnd < len(inner) && inner[vEnd] != '"' {
			if inner[vEnd] == '\\' {
				vEnd += 2
				continue
			}
			vEnd++
		}
		out[keyName] = string(inner[vStart+1 : vEnd])
		k = vEnd + 1
	}
	return out
}

func indexBytes(haystack, needle []byte) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// --- Types ---

type CanonicalMessage struct {
	Role    string
	Content string
}

type CanonicalReq struct {
	Messages []CanonicalMessage
	Model    string
	Stream   bool
	Headers  map[string]string
}

// --- Cline protocol ---

const (
	clineBaseURL     = "https://api.cline.bot"
	clineChatPath    = "/api/v1/chat/completions"
	clineUserAgent   = "Cline/3.0.60 ai-sdk/openai-compatible/3.0.30 ai-sdk/provider-utils/5.0.27 runtime/browser"
	clineClientType  = "cline-cli"
	clineClientVer   = "3.0.60"
	clineCoreVersion = "0.0.81"
	clinePlatform    = "cli"
	clinePlatformVer = "3.0.60"
)

// resolveTaskID keeps an inbound x-task-id when present; otherwise generates one.
func resolveTaskID(headers map[string]string) string {
	if headers != nil {
		for k, v := range headers {
			if len(k) == len("x-task-id") && (k == "x-task-id" || k == "X-Task-ID") {
				if v != "" {
					return v
				}
			}
		}
	}
	return fmt.Sprintf("%d_fg%x", time.Now().UnixMilli(), time.Now().UnixNano()%100000)
}

// clineAuthorization builds the Authorization header value.
// Cline's API rejects a plain `Bearer <token>`: OAuth tokens must carry the
// `workos:` prefix; BYOK API keys (sk_...) are sent as-is. Idempotent.
func clineAuthorization(token string) string {
	if token == "" {
		return ""
	}
	if len(token) > 7 && token[:7] == "workos:" {
		return "Bearer " + token
	}
	if len(token) > 3 && token[:3] == "sk_" {
		return "Bearer " + token
	}
	return "Bearer workos:" + token
}

// buildClineHeaders builds the Cline client-identification header set plus
// Authorization. Mirrors the header contract enforced by Cline upstream.
func buildClineHeaders(token, taskID string) string {
	auth := clineAuthorization(token)
	var b stringsBuilder
	b.write(`{`)
	b.write(`"Content-Type":"application/json",`)
	b.write(`"Accept":"*/*",`)
	b.write(`"http-referer":"https://cline.bot",`)
	b.write(`"HTTP-Referer":"https://cline.bot",`)
	b.write(`"x-title":"Cline",`)
	b.write(`"X-Title":"Cline",`)
	b.write(`"User-Agent":"` + jsonEscape(clineUserAgent) + `",`)
	b.write(`"x-is-multiroot":"false",`)
	b.write(`"X-IS-MULTIROOT":"false",`)
	b.write(`"x-client-type":"` + clineClientType + `",`)
	b.write(`"X-CLIENT-TYPE":"` + clineClientType + `",`)
	b.write(`"x-client-version":"` + clineClientVer + `",`)
	b.write(`"X-CLIENT-VERSION":"` + clineClientVer + `",`)
	b.write(`"x-core-version":"` + clineCoreVersion + `",`)
	b.write(`"X-CORE-VERSION":"` + clineCoreVersion + `",`)
	b.write(`"x-platform":"` + clinePlatform + `",`)
	b.write(`"X-PLATFORM":"` + clinePlatform + `",`)
	b.write(`"x-platform-version":"` + clinePlatformVer + `",`)
	b.write(`"X-PLATFORM-VERSION":"` + clinePlatformVer + `",`)
	b.write(`"x-task-id":"` + taskID + `",`)
	b.write(`"X-Task-ID":"` + taskID + `"`)
	if auth != "" {
		b.write(`,"Authorization":"` + jsonEscape(auth) + `"`)
	}
	b.write(`}`)
	return b.string()
}

// --- Chat ---

//export invoke
func invoke(reqPtr, reqLen uint32) uint32 {
	reqBytes := readLenPrefixed(reqPtr)
	if reqBytes == nil {
		return writeJSON(`{"error":"invalid request","code":"BAD_REQUEST"}`)
	}

	cap := extractJSONString(reqBytes, "capability")
	if cap == "oauth_authorize" {
		return oauthAuthorize(reqBytes)
	}
	if cap == "oauth_exchange" {
		return oauthExchange(reqBytes)
	}
	if cap == "oauth_refresh" {
		return oauthRefresh(reqBytes)
	}

	var req CanonicalReq
	req.Model = extractJSONString(reqBytes, "model")
	req.Stream = extractJSONBool(reqBytes, "stream")
	req.Messages = extractMessages(reqBytes)
	req.Headers = extractStringMap(reqBytes, "headers")
	if req.Model == "" {
		return writeJSON(`{"error":"invalid request: missing model","code":"BAD_REQUEST"}`)
	}

	creds := getCredentials()
	token := creds["api_key"]
	if token == "" {
		return writeJSON(`{"error":"no credentials","code":"NO_CREDENTIALS"}`)
	}
	baseURL := creds["base_url"]
	if baseURL == "" {
		baseURL = clineBaseURL
	}
	taskID := resolveTaskID(req.Headers)

	// Cline only implements streaming (streamText); a non-stream request returns
	// "generateText is not implemented". Always stream upstream and let the
	// non-stream path accumulate the SSE deltas below.
	msgsJSON := "["
	for i, m := range req.Messages {
		if i > 0 {
			msgsJSON += ","
		}
		msgsJSON += `{"role":"` + jsonEscape(m.Role) + `","content":"` + jsonEscape(m.Content) + `"}`
	}
	msgsJSON += "]"

	body := `{"model":"` + jsonEscape(req.Model) + `","stream":true,"messages":` + msgsJSON + `}`
	bodyPtr, bodyLen := stringToPtr(body)
	url := baseURL + clineChatPath
	urlPtr, urlLen := stringToPtr(url)
	hdrs := buildClineHeaders(token, taskID)
	hdrsPtr, hdrsLen := stringToPtr(hdrs)

	respPtr := hostHTTPPost(urlPtr, urlLen, bodyPtr, bodyLen, hdrsPtr, hdrsLen)
	if respPtr == 0 {
		return writeJSON(`{"error":"upstream request failed","code":"UPSTREAM_ERROR"}`)
	}
	respBytes := readLenPrefixed(respPtr)
	if respBytes == nil {
		return writeJSON(`{"error":"empty upstream response","code":"UPSTREAM_ERROR"}`)
	}

	// Host writes errors as {"error","code"} — surface them.
	if he := extractJSONString(respBytes, "error"); he != "" {
		code := extractJSONString(respBytes, "code")
		return writeJSON(fmt.Sprintf(`{"error":"%s","code":"%s"}`, jsonEscape(he), jsonEscape(code)))
	}

	// Cline may answer with an OpenAI-shaped error JSON (e.g. 401/429).
	if msg := extractJSONNestedString(respBytes, "error", "message"); msg != "" {
		return writeJSON(fmt.Sprintf(`{"error":"%s","code":"UPSTREAM_REJECTED"}`, jsonEscape(msg)))
	}

	var content stringsBuilder
	finish := "stop"
	streamed := req.Stream

	lines := splitLines(respBytes)
	for _, line := range lines {
		if !bytesStartsWith(line, []byte("data: ")) {
			continue
		}
		data := line[6:]
		if bytesEqual(data, []byte("[DONE]")) {
			break
		}
		// Manual parse: {"choices":[{"delta":{"content":"..."},"finish_reason":"..."}]}
		delta := extractJSONNestedString(data, "delta", "content")
		if fr := extractJSONString(data, "finish_reason"); fr != "" {
			finish = fr
		}
		if delta != "" {
			content.write(delta)
			if streamed {
				chunk := fmt.Sprintf(`{"choices":[{"delta":{"content":"%s"},"index":0}]}`, jsonEscape(delta))
				emit([]byte(chunk))
			}
		}
	}

	if streamed {
		chunk := fmt.Sprintf(`{"choices":[{"delta":{"content":""},"index":0,"finish_reason":"%s"}]}`, jsonEscape(finish))
		emit([]byte(chunk))
		return 0
	}

	return writeJSON(fmt.Sprintf(`{"content":"%s","finish_reason":"%s"}`, jsonEscape(content.string()), jsonEscape(finish)))
}

// --- OAuth (host-driven) ---
//
// The FlameGate host runs the browser redirect + loopback callback and calls
// these capabilities. All Cline/WorkOS-specific logic lives here in the guest.

const (
	clineAuthorizePath = "/api/v1/auth/authorize"
	clineTokenPath     = "/api/v1/auth/token"
	clineRefreshPath   = "/api/v1/auth/refresh"
)

// oauthAuthorize builds the WorkOS authorize URL the browser is redirected to.
func oauthAuthorize(reqBytes []byte) uint32 {
	redirectURI := extractJSONString(reqBytes, "redirect_uri")
	state := extractJSONString(reqBytes, "state")
	q := "client_type=extension"
	if callbackURL := firstOrEmpty(reqBytes, "callback_url"); callbackURL != "" {
		q += "&callback_url=" + callbackURL
	}
	if redirectURI != "" {
		q += "&redirect_uri=" + redirectURI + "&callback_url=" + redirectURI
	}
	if state != "" {
		q += "&state=" + state
	}
	return writeJSON(fmt.Sprintf(`{"url":"https://api.cline.bot%s?%s","state":"%s"}`, clineAuthorizePath, q, jsonEscape(state)))
}

// oauthExchange swaps a code (or decodes an embedded token) for credentials.
func oauthExchange(reqBytes []byte) uint32 {
	code := extractJSONString(reqBytes, "code")
	redirectURI := extractJSONString(reqBytes, "redirect_uri")
	if code == "" {
		return writeJSON(`{"error":"missing code","code":"BAD_REQUEST"}`)
	}

	// Cline embeds tokens as base64-encoded JSON in the auth code, or falls
	// back to a real token-exchange POST.
	if tok := decodeClineTokenCode(code); tok != nil {
		var b stringsBuilder
		b.write(`{`)
		first := true
		for k, v := range tok {
			if !first {
				b.write(`,`)
			}
			first = false
			b.write(`"` + jsonEscape(k) + `":"` + jsonEscape(v) + `"`)
		}
		b.write(`}`)
		return writeJSON(b.string())
	}

	body := `{"grant_type":"authorization_code","code":"` + jsonEscape(code) + `","client_type":"extension","redirect_uri":"` + jsonEscape(redirectURI) + `"}`
	return clineTokenRequest(clineTokenPath, body)
}

// oauthRefresh rotates an access token via the refresh endpoint.
func oauthRefresh(reqBytes []byte) uint32 {
	refreshToken := extractJSONString(reqBytes, "refresh_token")
	if refreshToken == "" {
		return writeJSON(`{"error":"no refresh_token","code":"BAD_REQUEST"}`)
	}
	body := `{"refreshToken":"` + jsonEscape(refreshToken) + `","grantType":"refresh_token","clientType":"extension"}`
	return clineTokenRequest(clineRefreshPath, body)
}

// clineTokenRequest POSTs to a Cline auth endpoint and maps the response.
func clineTokenRequest(path, body string) uint32 {
	url := clineBaseURL + path
	urlPtr, urlLen := stringToPtr(url)
	bodyPtr, bodyLen := stringToPtr(body)
	hdrs := buildClineHeaders("", "auth")
	hdrsPtr, hdrsLen := stringToPtr(hdrs)
	respPtr := hostHTTPPost(urlPtr, urlLen, bodyPtr, bodyLen, hdrsPtr, hdrsLen)
	if respPtr == 0 {
		return writeJSON(`{"error":"token request failed","code":"UPSTREAM_ERROR"}`)
	}
	respBytes := readLenPrefixed(respPtr)
	if respBytes == nil {
		return writeJSON(`{"error":"empty token response","code":"UPSTREAM_ERROR"}`)
	}
	if he := extractJSONString(respBytes, "error"); he != "" {
		code := extractJSONString(respBytes, "code")
		return writeJSON(fmt.Sprintf(`{"error":"%s","code":"%s"}`, jsonEscape(he), jsonEscape(code)))
	}
	return parseAndMapToken(respBytes)
}

// parseAndMapToken normalizes the token-exchange/refresh response into the
// shape the host expects (access_token / refresh_token / expires_at).
func parseAndMapToken(respBytes []byte) uint32 {
	at := extractJSONNestedString(respBytes, "data", "accessToken")
	if at == "" {
		at = extractJSONNestedString(respBytes, "data", "access_token")
	}
	if at == "" {
		at = extractJSONString(respBytes, "accessToken")
	}
	if at == "" {
		at = extractJSONString(respBytes, "access_token")
	}
	if at == "" {
		return writeJSON(`{"error":"no access_token in token response","code":"UPSTREAM_REJECTED"}`)
	}
	rt := extractJSONNestedString(respBytes, "data", "refreshToken")
	if rt == "" {
		rt = extractJSONNestedString(respBytes, "data", "refresh_token")
	}
	if rt == "" {
		rt = extractJSONString(respBytes, "refreshToken")
	}
	if rt == "" {
		rt = extractJSONString(respBytes, "refresh_token")
	}
	email := extractJSONNestedString(respBytes, "data", "email")
	if email == "" {
		email = extractJSONString(respBytes, "email")
	}
	expires := extractJSONNestedString(respBytes, "data", "expiresAt")
	if expires == "" {
		expires = extractJSONString(respBytes, "expires_at")
	}
	return writeJSON(fmt.Sprintf(`{"access_token":"%s","refresh_token":"%s","email":"%s","expires_at":"%s"}`,
		jsonEscape(at), jsonEscape(rt), jsonEscape(email), jsonEscape(expires)))
}

// decodeClineTokenCode tries to interpret a callback code as base64-encoded
// JSON carrying access/refresh tokens, returning nil if it does not decode.
func decodeClineTokenCode(code string) map[string]string {
	raw := strings.TrimSpace(code)
	if raw == "" {
		return nil
	}

	// Extract code value if a full URL or query string was supplied
	if idx := strings.Index(raw, "code="); idx != -1 {
		raw = raw[idx+5:]
		if amp := strings.Index(raw, "&"); amp != -1 {
			raw = raw[:amp]
		}
	}

	// Basic URL unescape for common percent encodings
	raw = strings.ReplaceAll(raw, "%3D", "=")
	raw = strings.ReplaceAll(raw, "%2B", "+")
	raw = strings.ReplaceAll(raw, "%2F", "/")

	var dec []byte
	var err error

	// Try standard base64 decoding with padding normalized
	padded := raw
	if pad := len(padded) % 4; pad != 0 {
		padded += strings.Repeat("=", 4-pad)
	}

	if d, e := base64.StdEncoding.DecodeString(padded); e == nil {
		dec = d
	} else if d, e := base64.URLEncoding.DecodeString(padded); e == nil {
		dec = d
	} else if d, e := base64.RawStdEncoding.DecodeString(raw); e == nil {
		dec = d
	} else if d, e := base64.RawURLEncoding.DecodeString(raw); e == nil {
		dec = d
	} else {
		return nil
	}

	// Find the trailing `}` and parse JSON.
	lastBrace := -1
	for i := len(dec) - 1; i >= 0; i-- {
		if dec[i] == '}' {
			lastBrace = i
			break
		}
	}
	if lastBrace == -1 {
		return nil
	}
	var m map[string]string
	if err = json.Unmarshal(dec[:lastBrace+1], &m); err != nil {
		return nil
	}
	at := m["accessToken"]
	if at == "" {
		at = m["access_token"]
	}
	if rt := m["refreshToken"]; rt != "" {
		m["refresh_token"] = rt
	}
	m["access_token"] = at
	m["expires_at"] = m["expiresAt"]
	return m
}

// firstOrEmpty is a tiny helper to read a JSON string key.
func firstOrEmpty(data []byte, key string) string {
	return extractJSONString(data, key)
}

// jsonEscape escapes a string for embedding in a JSON literal.
func jsonEscape(s string) string {
	out := ""
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			out += `\"`
		case '\\':
			out += `\\`
		case '\n':
			out += `\n`
		case '\r':
			out += `\r`
		case '\t':
			out += `\t`
		default:
			out += string(s[i])
		}
	}
	return out
}

// --- Models ---

//export list_models
func list_models() uint32 {
	// Offline curated catalog declaring explicit tiers and tags for reference
	const raw = `[` +
		`{"id":"moonshotai/kimi-k3","name":"Kimi K3 (Frontier)","tier":"frontier","tags":["frontier","chat"]},` +
		`{"id":"anthropic/claude-opus-5","name":"Claude Opus 5 (Frontier)","tier":"frontier","tags":["frontier","thinking"]},` +
		`{"id":"x-ai/grok-4.5","name":"Grok 4.5 (Frontier)","tier":"frontier","tags":["frontier"]},` +
		`{"id":"openai/gpt-5.6-sol","name":"GPT-5.6 Sol (Frontier)","tier":"frontier","tags":["frontier"]},` +
		`{"id":"zai/glm-5.2","name":"GLM 5.2","tier":"pro","tags":["pro"]},` +
		`{"id":"z-ai/glm-5.3-flash","name":"GLM 5.3 Flash (Free)","tier":"free","tags":["free","flash"]},` +
		`{"id":"deepseek/deepseek-v4-flash","name":"DeepSeek V4 Flash (Free)","tier":"free","tags":["free","flash"]},` +
		`{"id":"poolside/laguna-s-2.1:free","name":"Laguna S 2.1 (Free)","tier":"free","tags":["free","code"]},` +
		`{"id":"openrouter/free","name":"Free Models Router","tier":"free","tags":["free","router"]},` +
		`{"id":"tencent/hy3:free","name":"Tencent Hy3 (Free)","tier":"free","tags":["free"]},` +
		`{"id":"stepfun/step-3.7-flash","name":"Step 3.7 Flash (Free)","tier":"free","tags":["free","flash"]},` +
		`{"id":"poolside/laguna-m.1:free","name":"Laguna M.1 (Free)","tier":"free","tags":["free","code"]},` +
		`{"id":"google/gemma-4-31b-it:free","name":"Gemma 4 31B (Free)","tier":"free","tags":["free"]},` +
		`{"id":"nvidia/nemotron-3-ultra-550b-a55b:free","name":"Nemotron 3 Ultra (Free)","tier":"free","tags":["free"]},` +
		`{"id":"minimax/minimax-m3","name":"MiniMax M3 (Free)","tier":"free","tags":["free"]},` +
		`{"id":"cline-pass/qwen3.8-max","name":"Qwen 3.8 Max (ClinePass)","tier":"pass","tags":["pass","code"]},` +
		`{"id":"cline-pass/qwen3.7-max","name":"Qwen 3.7 Max (ClinePass)","tier":"pass","tags":["pass","code"]},` +
		`{"id":"cline-pass/qwen3.7-plus","name":"Qwen 3.7 Plus (ClinePass)","tier":"pass","tags":["pass","code"]},` +
		`{"id":"cline-pass/deepseek-v4-pro","name":"DeepSeek V4 Pro (ClinePass)","tier":"pass","tags":["pass","pro"]},` +
		`{"id":"cline-pass/deepseek-v4-flash","name":"DeepSeek V4 Flash (ClinePass)","tier":"pass","tags":["pass","flash"]},` +
		`{"id":"cline-pass/kimi-k3","name":"Kimi K3 (ClinePass)","tier":"pass","tags":["pass","frontier"]},` +
		`{"id":"cline-pass/kimi-k2.7-code","name":"Kimi K2.7 Code (ClinePass)","tier":"pass","tags":["pass","code"]},` +
		`{"id":"cline-pass/kimi-k2.6","name":"Kimi K2.6 (ClinePass)","tier":"pass","tags":["pass","code"]},` +
		`{"id":"cline-pass/glm-5.3","name":"GLM 5.3 (ClinePass)","tier":"pass","tags":["pass"]},` +
		`{"id":"cline-pass/glm-5.2","name":"GLM 5.2 (ClinePass)","tier":"pass","tags":["pass"]},` +
		`{"id":"cline-pass/minimax-m3","name":"MiniMax M3 (ClinePass)","tier":"pass","tags":["pass"]},` +
		`{"id":"cline-pass/mimo-v2.5-pro","name":"MiMo V2.5 Pro (ClinePass)","tier":"pass","tags":["pass","pro"]},` +
		`{"id":"cline-pass/mimo-v2.5","name":"MiMo V2.5 (ClinePass)","tier":"pass","tags":["pass"]},` +
		`{"id":"cline-cloud/kimi-k3","name":"Kimi K3 (Cloud)","tier":"pro","tags":["pro","cloud"]},` +
		`{"id":"cline-cloud/deepseek-v4-flash","name":"DeepSeek V4 Flash (Cloud)","tier":"flash","tags":["flash","cloud"]},` +
		`{"id":"cline-cloud/glm-5.2","name":"GLM 5.2 (Cloud)","tier":"pro","tags":["pro","cloud"]}` +
		`]`
	return writeJSON(raw)
}

// --- Helpers ---

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// stringsBuilder is a tiny string accumulator (strings.Builder is heavy on TinyGo).
type stringsBuilder struct {
	buf []byte
}

func (s *stringsBuilder) write(v string) {
	s.buf = append(s.buf, v...)
}

func (s *stringsBuilder) string() string {
	return string(s.buf)
}

func splitLines(b []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			line := b[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
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
