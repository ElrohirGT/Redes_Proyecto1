package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	ant "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pelletier/go-toml/v2"
)

const HELP_CONTENT = `
F1: Toggle Help
F2: Toggle Logs
`

const LOG_FILE = "session.log"

const GAP = "\n\n"

type MCPServerType string

var MCP_SERVERS_TYPE = struct {
	Http  MCPServerType
	Stdio MCPServerType
}{
	Http:  "http",
	Stdio: "stdio",
}

type MCPServerConfig struct {
	Name     string
	Type     MCPServerType
	Endpoint string
	URL      string
	Command  string
	Args     []string
}

type Config struct {
	MaxTokens uint
	Servers   []MCPServerConfig
}

var LOG *log.Logger

type ClaudeResponse = *ant.Message
type ToolResponse struct {
	IsError     bool
	MCPResponse *mcp.CallToolResult
	ToolId      string
}

func main() {
	logfile, err := os.OpenFile(LOG_FILE, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}
	defer logfile.Close()
	LOG = log.New(logfile, "CLIude: ", log.LstdFlags)

	err = godotenv.Load()
	if err != nil {
		LOG.Panic(err)
	}

	apiKey, exists := os.LookupEnv("API_KEY")
	if !exists {
		LOG.Panic("Env variable `API_KEY` doesn't exists!")
	}

	// ghPAT, exists := os.LookupEnv("GITHUB_")
	// if !exists {
	// 	LOG.Panic("Env variable `API_KEY` doesn't exists!")
	// }

	contents, err := os.ReadFile("config.toml")
	if err != nil {
		LOG.Panic("Failed to read config file:", err)
	}

	var config Config
	err = toml.Unmarshal(contents, &config)
	if err != nil {
		LOG.Panic("Incorrect format in config:", err)
	}

	wg := sync.WaitGroup{}
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered in main:\n%s", r)
		}

		LOG.Printf("Waiting for goroutines to finish...")
		wg.Wait()
		LOG.Printf("Goodbye!")
	}()

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	p := tea.NewProgram(initialModel(ctx, &wg, apiKey, config))
	if _, err := p.Run(); err != nil {
		LOG.Fatal(err)
	}

}

type model struct {
	maxTokens        uint
	wg               *sync.WaitGroup
	programCtx       context.Context
	secondaryDisplay bool
	aiThinking       bool
	viewport         viewport.Model
	messages         []ant.MessageParam
	textarea         textarea.Model
	senderStyle      lipgloss.Style

	// AI AGENTS PROPERTIES
	claudeClient     ant.Client
	mcpClients       []*client.Client
	tools            []ant.ToolUnionParam
	clientByToolName map[string]*client.Client
	err              error
}

func initialModel(
	ctx context.Context,
	wg *sync.WaitGroup,
	apiKey string,
	config Config,
) model {
	ta := textarea.New()
	ta.Placeholder = "Send a message..."
	ta.Focus()

	ta.Prompt = "| "
	ta.CharLimit = 500

	ta.SetWidth(30)
	ta.SetHeight(3)

	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)

	vp := viewport.New(30, 5)
	vp.SetContent("Welcome! Chat to claude...\nPress F1 to view help!")

	antClient := ant.NewClient(
		option.WithAPIKey(apiKey),
	)

	tools := make([]ant.ToolUnionParam, 0, len(config.Servers))
	clientByToolName := make(map[string]*client.Client)
	mcpClients := make([]*client.Client, 0, len(config.Servers))
	for _, clientConfig := range config.Servers {
		var err error
		var trans transport.Interface
		if clientConfig.Type == MCP_SERVERS_TYPE.Http {
			trans, err = transport.NewStreamableHTTP(
				clientConfig.URL,
			)
			if err != nil {
				LOG.Printf("Failed to connect to `%s`: %s", clientConfig.URL, err)
			}
			LOG.Println("Connecting to (http) client:", clientConfig.URL)
		} else {
			trans = transport.NewStdio(clientConfig.Command, os.Environ(), clientConfig.Args...)
			LOG.Println("Connecting to (stdio) client:", clientConfig.Command)
		}

		mcpClient := client.NewClient(trans)
		err = mcpClient.Start(ctx)
		if err != nil {
			LOG.Printf("Failed to start client `%#v`: %s", clientConfig, err)
			continue
		}

		mcpClient.OnNotification(func(notification mcp.JSONRPCNotification) {
			LOG.Printf("Client `%#v` notification: %s", clientConfig, notification.Method)
		})

		LOG.Printf("Initializing client!")
		capabilities, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{
			Params: mcp.InitializeParams{
				ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
				ClientInfo: mcp.Implementation{
					Name:    "CLIude",
					Version: "1.0.0",
				},
				Capabilities: mcp.ClientCapabilities{},
			},
		})
		if err != nil {
			LOG.Panicf("Failed to connect to client: %s", err)
		}
		LOG.Printf("Server capabilities:\n%#v", capabilities)
		mcpClients = append(mcpClients, mcpClient)

		if capabilities.Capabilities.Tools != nil {
			var defaultCursor mcp.Cursor
			var cursor mcp.Cursor
			for {
				svTools, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{
					PaginatedRequest: mcp.PaginatedRequest{
						Params: mcp.PaginatedParams{
							Cursor: cursor,
						},
					},
				})
				if err != nil {
					LOG.Panicf("Failed to obtain tools for client: %s", err)
				}

				for _, tool := range svTools.Tools {
					LOG.Printf("Adding tool: %s - %s\nThe tool has the following schema:\n%#v", tool.Name, tool.Description, tool.InputSchema)

					clientByToolName[tool.Name] = mcpClient
					tools = append(tools, ant.ToolUnionParam{
						OfTool: &ant.ToolParam{
							Name:        tool.Name,
							Description: param.NewOpt(tool.Description),
							InputSchema: ant.ToolInputSchemaParam{
								Required:   tool.InputSchema.Required,
								Properties: tool.InputSchema.Properties,
							},
						},
					})
				}

				if svTools.NextCursor == defaultCursor {
					break // No more pages
				}
				cursor = svTools.NextCursor
			}
		}
	}

	return model{
		maxTokens:        config.MaxTokens,
		wg:               wg,
		programCtx:       ctx,
		textarea:         ta,
		viewport:         vp,
		senderStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		claudeClient:     antClient,
		mcpClients:       mcpClients,
		clientByToolName: clientByToolName,
		err:              nil,
		tools:            tools,
	}
}

func (m model) StringMessages() []string {
	messages := make([]string, 0, len(m.messages))
	for _, msg := range m.messages {
		strMsg := strings.Builder{}
		author := "You:"
		if msg.Role == "assistant" {
			author = "Claude:"
		}

		strMsg.WriteString(m.senderStyle.Render(author))
		strMsg.WriteRune(' ')
		for _, ct := range msg.Content {
			if param.IsOmitted(ct) {
				continue
			}

			if ct.OfText != nil {
				strMsg.WriteString(ct.OfText.Text)
			} else if ct.OfToolUse != nil {
				toolName := ct.OfToolUse.Name
				strMsg.WriteString(" (Trying to use tool `")
				strMsg.WriteString(toolName)
				strMsg.WriteString("`)")
			} else if ct.OfToolResult != nil {
				if ct.OfToolResult.IsError.Value {
					strMsg.WriteString(" (Failed to use tool!)")
				} else {
					strMsg.WriteString(" (Used tool successfully!)")
				}
			} else if ct.OfThinking != nil {
				strMsg.WriteString(" (AI is thinking...)")
			} else {
				strMsg.WriteString(" (Can't display block type on terminal!)")
			}
		}

		messages = append(messages, strMsg.String())
	}
	return messages
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		taCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textarea, taCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)
	viewportWidthStyle := lipgloss.NewStyle().Width(m.viewport.Width)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.textarea.SetWidth(msg.Width)
		m.viewport.Height = msg.Height - m.textarea.Height() - lipgloss.Height(GAP)

		if len(m.messages) > 0 {
			// Wrap content before setting it.
			m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.StringMessages(), "\n")))
		}
		m.viewport.GotoBottom()
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyF1:
			m.secondaryDisplay = !m.secondaryDisplay
			if m.secondaryDisplay {
				m.viewport.SetContent(viewportWidthStyle.Render(HELP_CONTENT))
			} else {
				m.viewport.SetContent(viewportWidthStyle.Render(strings.Join(m.StringMessages(), "\n")))
			}
		case tea.KeyF2:
			m.secondaryDisplay = !m.secondaryDisplay
			if m.secondaryDisplay {
				logsContent := []byte{}
				if file, err := os.Open(LOG_FILE); err == nil {
					logsContent, _ = io.ReadAll(file)
				}
				m.viewport.SetContent(viewportWidthStyle.Render(string(logsContent)))
			} else {
				m.viewport.SetContent(viewportWidthStyle.Render(strings.Join(m.StringMessages(), "\n")))
			}

		case tea.KeyEnter:
			userMsg := m.textarea.Value()
			authorMsg := ant.NewUserMessage(
				ant.NewTextBlock(userMsg),
			)

			m.messages = append(m.messages, authorMsg)
			claudeCmd := claudeCall(m.programCtx, &m)
			m.messages = append(m.messages, ant.MessageParam{
				Role: "assistant",
				Content: []ant.ContentBlockParamUnion{
					ant.NewThinkingBlock("", ""),
				},
			})

			m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.StringMessages(), "\n")))
			m.textarea.Reset()
			m.viewport.GotoBottom()
			return m, tea.Batch(taCmd, vpCmd, claudeCmd)
		}

	// We handle errors just like any other message
	case error:
		m.aiThinking = false
		m.err = msg
		return m, nil
	case ClaudeResponse:
		m.aiThinking = false
		m.messages[len(m.messages)-1] = msg.ToParam() // Replaces last message with Claude real response
		m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.StringMessages(), "\n")))
		m.viewport.GotoBottom()

		if msg.StopReason == ant.StopReasonToolUse {
			toolBlock := msg.Content[len(msg.Content)-1].AsToolUse()
			toolName := toolBlock.Name
			client, found := m.clientByToolName[toolName]
			if !found {
				LOG.Panic("Claude tried to use", toolName, ". But this tool doesn't exist!")
			}

			toolCmd := toolCall(m.programCtx, client, toolBlock)
			return m, tea.Batch(taCmd, vpCmd, toolCmd)
		}

	case ToolResponse:
		blocks := make([]ant.ContentBlockParamUnion, 0, len(msg.MCPResponse.Content))
		for _, ct := range msg.MCPResponse.Content {
			switch ct := ct.(type) {
			case mcp.TextContent:
				resultBlock := ant.NewToolResultBlock(msg.ToolId, ct.Text, strings.Contains(ct.Text, "Error"))
				blocks = append(blocks, resultBlock)
			default:
				LOG.Printf("Unsupported block type for tool response! %#v", ct)
			}
		}
		toolResponse := ant.NewUserMessage()
		toolResponse.Content = blocks

		m.messages = append(m.messages, toolResponse)
		claudeCmd := claudeCall(m.programCtx, &m)
		m.messages = append(m.messages, ant.MessageParam{
			Role: "assistant",
			Content: []ant.ContentBlockParamUnion{
				ant.NewThinkingBlock("", ""),
			},
		})

		m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.StringMessages(), "\n")))
		m.viewport.GotoBottom()
		return m, tea.Batch(taCmd, vpCmd, claudeCmd)
	}

	return m, tea.Batch(taCmd, vpCmd)
}

func toolCall(ctx context.Context, client *client.Client, toolInfo ant.ToolUseBlock) tea.Cmd {
	ctx, cancelCtx := context.WithTimeout(ctx, 10*time.Second)
	return func() tea.Msg {
		defer cancelCtx()

		bytes, err := toolInfo.Input.MarshalJSON()
		if err != nil {
			LOG.Panicf("Failed to format: %#v: %s", toolInfo.Input, err)
		}

		params := map[string]any{}
		err = json.Unmarshal(bytes, &params)
		if err != nil {
			LOG.Panicf("Failed to unmarshall into a map: %s\n%s", err, string(bytes))
		}

		LOG.Printf("Calling tool `%s` for response with:\n%#v", toolInfo.Name, params)
		resp, err := client.CallTool(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      toolInfo.Name,
				Arguments: params,
			},
		})
		if err != nil {
			LOG.Println("ERROR: Failed to call tool:", err)
			return err
		}

		return ToolResponse{
			IsError:     false,
			MCPResponse: resp,
			ToolId:      toolInfo.ID,
		}
	}
}

func claudeCall(ctx context.Context, m *model) tea.Cmd {
	ctx, cancelCtx := context.WithTimeout(ctx, 10*time.Minute)
	messages := m.messages
	return func() tea.Msg {
		defer cancelCtx()
		m.aiThinking = true

		LOG.Println("Calling claude for response...")
		message, err := m.claudeClient.Messages.New(ctx, ant.MessageNewParams{
			MaxTokens: int64(m.maxTokens),
			Messages:  messages,
			Model:     ant.ModelClaudeSonnet4_20250514,
			Tools:     m.tools,
		},
			option.WithDebugLog(LOG),
		)

		if err != nil {
			LOG.Println("Failed to get response from claude:", err)
			return err
		} else {
			LOG.Println("Claude responded correctly!")
			return message
		}
	}
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf(
			"%s\n%s\n%s",
			m.viewport.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Render("ERROR: ")+m.err.Error(),
			m.textarea.View(),
		)
	} else {
		return fmt.Sprintf(
			"%s%s%s",
			m.viewport.View(),
			GAP,
			m.textarea.View(),
		)
	}
}
