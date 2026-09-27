package acpagent

import "encoding/json"

// protocolVersion is the one ACP major version this client speaks.
const protocolVersion = 1

const (
	clientName    = "atlas"
	clientVersion = "0"
)

type initializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
	ClientInfo         implementation     `json:"clientInfo"`
}

// clientCapabilities advertises no file system and no terminal: the agent
// uses its own, and reaches Atlas only through MCP.
type clientCapabilities struct {
	FS       fsCapabilities `json:"fs"`
	Terminal bool           `json:"terminal"`
}

type fsCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities agentCapabilities `json:"agentCapabilities"`
}

type agentCapabilities struct {
	LoadSession     bool            `json:"loadSession"`
	MCPCapabilities mcpCapabilities `json:"mcpCapabilities"`
}

type mcpCapabilities struct {
	HTTP bool `json:"http"`
}

type mcpServer struct {
	Type    string       `json:"type"`
	Name    string       `json:"name"`
	URL     string       `json:"url"`
	Headers []httpHeader `json:"headers"`
}

type httpHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type newSessionParams struct {
	CWD        string      `json:"cwd"`
	MCPServers []mcpServer `json:"mcpServers"`
}

type newSessionResult struct {
	SessionID string `json:"sessionId"`
}

type loadSessionParams struct {
	SessionID  string      `json:"sessionId"`
	CWD        string      `json:"cwd"`
	MCPServers []mcpServer `json:"mcpServers"`
}

type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
}

type cancelParams struct {
	SessionID string `json:"sessionId"`
}

type sessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    sessionUpdate `json:"update"`
}

// sessionUpdate is every session/update variant in one shape. Content is
// raw because a message chunk carries one content block and a tool call
// carries a list.
type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content,omitempty"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	Name          string          `json:"name,omitempty"`
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
	Entries       []planEntry     `json:"entries,omitempty"`
}

type planEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type permissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  sessionUpdate      `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type permissionResult struct {
	Outcome permissionOutcome `json:"outcome"`
}

type permissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}
