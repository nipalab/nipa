package suite_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations json.RawMessage `json:"annotations"`
}

type mcpToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

func (r mcpToolResult) text() string {
	var parts []string
	for _, content := range r.Content {
		parts = append(parts, content.Text)
	}
	return strings.Join(parts, "\n")
}

func (r mcpToolResult) decode(out any) {
	GinkgoHelper()
	Expect(r.StructuredContent).NotTo(BeEmpty(), "no structured content: %s", r.text())
	Expect(json.Unmarshal(r.StructuredContent, out)).To(Succeed())
}

type mcpProc struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	logPath string
	nextID  int
	done    chan struct{}
	info    struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
}

func startMCP(repoDir string, allowWrite bool) *mcpProc {
	GinkgoHelper()
	return startMCPWithTokenFile(repoDir, allowWrite, cli.tokenFile)
}

func startMCPWithTokenFile(repoDir string, allowWrite bool, tokenFile string) *mcpProc {
	GinkgoHelper()

	args := []string{"mcp", "--repo", repoDir}
	if allowWrite {
		args = append(args, "--allow-write")
	}

	logPath := filepath.Join(GinkgoT().TempDir(), "mcp.log")
	logFile, err := os.Create(logPath)
	Expect(err).NotTo(HaveOccurred())

	cmd := exec.Command(cli.binary, args...)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "NIPA_TOKEN_FILE="+tokenFile)
	stdin, err := cmd.StdinPipe()
	Expect(err).NotTo(HaveOccurred())
	stdout, err := cmd.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	cmd.Stderr = logFile
	Expect(cmd.Start()).To(Succeed())

	m := &mcpProc{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		logPath: logPath,
		done:    make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(m.done)
	}()
	DeferCleanup(m.stop)
	m.initialize()
	return m
}

func (m *mcpProc) stop() {
	_ = m.stdin.Close()
	select {
	case <-m.done:
	case <-time.After(3 * time.Second):
		_ = m.cmd.Process.Kill()
		<-m.done
	}
}

func (m *mcpProc) logTail() string {
	data, _ := os.ReadFile(m.logPath)
	return string(data)
}

func (m *mcpProc) send(method string, params any, withID bool) {
	GinkgoHelper()
	request := map[string]any{"jsonrpc": "2.0", "method": method}
	if withID {
		m.nextID++
		request["id"] = m.nextID
	}
	if params != nil {
		request["params"] = params
	}
	data, err := json.Marshal(request)
	Expect(err).NotTo(HaveOccurred())
	_, err = m.stdin.Write(append(data, '\n'))
	Expect(err).NotTo(HaveOccurred())
}

func (m *mcpProc) read() mcpMessage {
	GinkgoHelper()
	for {
		line, err := m.stdout.ReadString('\n')
		Expect(err).NotTo(HaveOccurred(), "mcp stderr: %s", m.logTail())
		var msg mcpMessage
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		return msg
	}
}

func (m *mcpProc) request(method string, params any) mcpMessage {
	GinkgoHelper()
	m.send(method, params, true)
	want := fmt.Sprintf("%d", m.nextID)
	for {
		msg := m.read()
		if string(msg.ID) == want {
			return msg
		}
	}
}

func (m *mcpProc) initialize() {
	GinkgoHelper()
	resp := m.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "nipa-e2e", "version": "0.0.1"},
	})
	Expect(resp.Error).To(BeNil())
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	Expect(json.Unmarshal(resp.Result, &result)).To(Succeed())
	Expect(result.ProtocolVersion).NotTo(BeEmpty())
	m.info = result.ServerInfo
	m.send("notifications/initialized", map[string]any{}, false)
}

func (m *mcpProc) tools() []mcpTool {
	GinkgoHelper()
	resp := m.request("tools/list", map[string]any{})
	Expect(resp.Error).To(BeNil())
	var result struct {
		Tools []mcpTool `json:"tools"`
	}
	Expect(json.Unmarshal(resp.Result, &result)).To(Succeed())
	return result.Tools
}

func (m *mcpProc) toolNames() []string {
	GinkgoHelper()
	names := []string{}
	for _, tool := range m.tools() {
		names = append(names, tool.Name)
	}
	return names
}

func (m *mcpProc) callTool(name string, args map[string]any) mcpToolResult {
	GinkgoHelper()
	resp := m.request("tools/call", map[string]any{"name": name, "arguments": args})
	Expect(resp.Error).To(BeNil(), "tools/call transport error: %+v", resp.Error)
	var result mcpToolResult
	Expect(json.Unmarshal(resp.Result, &result)).To(Succeed())
	return result
}
