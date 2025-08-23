package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	ant "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joho/godotenv"
	mcp "github.com/metoro-io/mcp-golang"
	"github.com/metoro-io/mcp-golang/transport/http"
	"github.com/pelletier/go-toml/v2"
)

const HELP_CONTENT = `
F1: View Help
F2: View Logs
`

const LOG_FILE = "session.log"

const GAP = "\n\n"

type MCPServerConfig struct {
	Endpoint string
	URL      string
}

type Config struct {
	Servers []MCPServerConfig
}

var LOG *log.Logger

type ClaudeResponse = *ant.Message
type ToolResponse struct {
	IsError     bool
	MCPResponse *mcp.ToolResponse
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

	contents, err := os.ReadFile("config.toml")
	if err != nil {
		LOG.Panic("Failed to read config file:", err)
	}

	var config Config
	err = toml.Unmarshal(contents, &config)
	if err != nil {
		LOG.Panic("Incorrect format in config:", err)
	}

	p := tea.NewProgram(initialModel(apiKey, config))
	if _, err := p.Run(); err != nil {
		LOG.Fatal(err)
	}
}

type model struct {
	secondaryDisplay bool
	aiThinking       bool
	viewport         viewport.Model
	messages         []ant.MessageParam
	textarea         textarea.Model
	senderStyle      lipgloss.Style

	// AI AGENTS PROPERTIES
	claudeClient     ant.Client
	mcpClients       []*mcp.Client
	tools            []ant.ToolUnionParam
	clientByToolName map[string]*mcp.Client
	err              error
}

func initialModel(apiKey string, config Config) model {
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

	client := ant.NewClient(
		option.WithAPIKey(apiKey),
	)

	tools := make([]ant.ToolUnionParam, 0, len(config.Servers))
	clientByToolName := make(map[string]*mcp.Client)
	mcpClients := make([]*mcp.Client, 0, len(config.Servers))
	for _, clientConfig := range config.Servers {
		transport := http.NewHTTPClientTransport(clientConfig.Endpoint)
		transport.WithBaseURL(clientConfig.URL)

		LOG.Println("Connecting to client:", clientConfig.URL)
		client := mcp.NewClient(transport)
		_, err := client.Initialize(context.Background())
		if err != nil {
			LOG.Panicf("Failed to connect to %s: %s", clientConfig.URL, err)
		}
		mcpClients = append(mcpClients, client)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		svTools, err := client.ListTools(ctx, nil)
		if err != nil {
			LOG.Panicf("Failed to obtain tools for %s: %s", clientConfig.URL, err)
		}

		for _, tool := range svTools.Tools {
			desc := ""
			if tool.Description != nil {
				desc = *tool.Description
			}
			LOG.Println("Adding tool:", tool.Name, "-", desc)
			LOG.Printf("The tool has the following schema:\n%#v", tool.InputSchema)
			clientByToolName[tool.Name] = client
			schema := tool.InputSchema.(map[string]any)
			schemaRequired := schema["required"].([]any)
			required := make([]string, 0, len(schemaRequired))
			for _, v := range schemaRequired {
				required = append(required, v.(string))
			}
			tools = append(tools, ant.ToolUnionParam{
				OfTool: &ant.ToolParam{
					Name:        tool.Name,
					Description: param.NewOpt(desc),
					InputSchema: ant.ToolInputSchemaParam{
						Required:   required,
						Properties: schema["properties"],
					},
				},
			})
		}
	}

	return model{
		textarea:         ta,
		viewport:         vp,
		senderStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		claudeClient:     client,
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
			claudeCmd := claudeCall(context.Background(), &m)
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
				LOG.Panic("Claude tried to use", toolName, ". But this tool doens't exist!")
			}

			toolCmd := toolCall(context.Background(), client, toolBlock)
			return m, tea.Batch(taCmd, vpCmd, toolCmd)
		}

	case ToolResponse:
		blocks := make([]ant.ContentBlockParamUnion, 0, len(msg.MCPResponse.Content))
		for _, ct := range msg.MCPResponse.Content {
			resultBlock := ant.NewToolResultBlock(msg.ToolId, ct.TextContent.Text, strings.Contains(ct.TextContent.Text, "Error"))
			blocks = append(blocks, resultBlock)
		}
		toolResponse := ant.NewUserMessage()
		toolResponse.Content = blocks

		m.messages = append(m.messages, toolResponse)
		claudeCmd := claudeCall(context.Background(), &m)
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

func toolCall(ctx context.Context, client *mcp.Client, toolInfo ant.ToolUseBlock) tea.Cmd {
	ctx, cancelCtx := context.WithTimeout(ctx, 10*time.Second)
	return func() tea.Msg {
		defer cancelCtx()

		LOG.Printf("Calling tool `%s` for response with:\n%#v", toolInfo.Name, toolInfo.Input)
		resp, err := client.CallTool(ctx, toolInfo.Name, toolInfo.Input)
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
			MaxTokens: 1024,
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
			"%s%s%s%s",
			m.viewport.View(),
			"ERROR: "+m.err.Error(),
			"\n",
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
