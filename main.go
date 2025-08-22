package main

import (
	"fmt"
	"log"
	"os"
	"strings"

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

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Panic(err)
	}

	apiKey, exists := os.LookupEnv("API_KEY")
	if !exists {
		log.Panic("Env variable `API_KEY` doesn't exists!")
	}

	contents, err := os.ReadFile("config.toml")
	if err != nil {
		log.Panic("Failed to read config file:", err)
	}

	var config Config
	err = toml.Unmarshal(contents, &config)
	if err != nil {
		log.Panic("Incorrect format in config:", err)
	}

	p := tea.NewProgram(initialModel(apiKey, config))
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
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
	claudeClient ant.Client
	mcpClients   []*mcp.Client
	err          error
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
	vp.SetContent("Welcome! Chat to claude...")

	client := ant.NewClient(
		option.WithAPIKey(apiKey),
	)

	mcpClients := make([]*mcp.Client, 0, len(config.Servers))
	for _, clientConfig := range config.Servers {
		transport := http.NewHTTPClientTransport(clientConfig.Endpoint)
		transport.WithBaseURL(clientConfig.URL)
		log.Println("Connecting to client:", clientConfig.URL)
		mcpClients = append(mcpClients, mcp.NewClient(transport))
	}

	return model{
		textarea:     ta,
		viewport:     vp,
		senderStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		claudeClient: client,
		mcpClients:   mcpClients,
		err:          nil,
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
