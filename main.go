package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	ant "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joho/godotenv"
	mcp "github.com/metoro-io/mcp-golang"
	"github.com/metoro-io/mcp-golang/transport/http"
	"github.com/pelletier/go-toml/v2"
)

const GAP = "\n\n"

type MCPServerConfig struct {
	Endpoint string
	URL      string
}

type Config struct {
	Servers []MCPServerConfig
}

var LOG *log.Logger

func main() {
	logfile, err := os.OpenFile("app.log", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}
	defer logfile.Close()
	LOG = log.New(logfile, "INFO", log.LstdFlags)

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

type viewportMsg struct {
	content string
	author  string
}

type model struct {
	viewport    viewport.Model
	messages    []viewportMsg
	textarea    textarea.Model
	senderStyle lipgloss.Style

	// AI AGENTS PROPERTIES
	claudeClient     ant.Client
	mcpClients       []*mcp.Client
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
		tools, err := client.ListTools(ctx, nil)
		if err != nil {
			LOG.Panicf("Failed to obtain tools for %s: %s", clientConfig.URL, err)
		}

		for _, tool := range tools.Tools {
			LOG.Println("Adding tool:", tool.Name, "-", *tool.Description)
			clientByToolName[tool.Name] = client
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
	}
}

func (m model) StringMessages() []string {
	a := make([]string, 0, len(m.messages))
	for _, v := range m.messages {
		a = append(a, m.senderStyle.Render(v.author+": ")+v.content)
	}
	return a
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
		case tea.KeyEnter:
			m.messages = append(m.messages, viewportMsg{content: m.textarea.Value(), author: "You"})
			m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.StringMessages(), "\n")))
			m.textarea.Reset()
			m.viewport.GotoBottom()
		}

	// We handle errors just like any other message
	case error:
		m.err = msg
		return m, nil
	}

	return m, tea.Batch(taCmd, vpCmd)
}

func (m model) View() string {
	return fmt.Sprintf(
		"%s%s%s",
		m.viewport.View(),
		"\n\n",
		m.textarea.View(),
	)
}
