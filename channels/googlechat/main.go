// Package main is the entry point for the Google Chat channel pod.
// It uses the Google Chat API with support for interactive cards,
// slash commands, threaded replies, and rich formatting.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/alexsjones/sympozium/internal/channel"
	"github.com/alexsjones/sympozium/internal/eventbus"
	"github.com/alexsjones/sympozium/pkg/gcpauth"
	"github.com/alexsjones/sympozium/pkg/telemetry"
	"github.com/go-logr/logr"
)

var gchatTracer = otel.Tracer("sympozium.ai/channel-googlechat")

// GoogleChatChannel implements the Google Chat channel with rich card support.
type GoogleChatChannel struct {
	channel.BaseChannel
	log       logr.Logger
	client    *http.Client
	tokenSrc  *gcpauth.CacheableTokenSource
	projectID string
	healthy   bool
	mu        sync.RWMutex
}

// --- Google Chat API types ---

// ChatEvent represents an incoming Google Chat event.
type ChatEvent struct {
	Type           string         `json:"type"`
	EventTime      string         `json:"eventTime"`
	Message        *ChatMessage   `json:"message,omitempty"`
	Space          *ChatSpace     `json:"space,omitempty"`
	User           *ChatUser      `json:"user,omitempty"`
	Action         *ChatAction    `json:"action,omitempty"`
	ConfigComplete *struct{}      `json:"configCompleteRedirectUrl,omitempty"`
	Common         *ChatCommon    `json:"common,omitempty"`
}

// ChatMessage is a message in Google Chat.
type ChatMessage struct {
	Name         string      `json:"name,omitempty"`
	Sender       *ChatUser   `json:"sender,omitempty"`
	CreateTime   string      `json:"createTime,omitempty"`
	Text         string      `json:"text,omitempty"`
	Thread       *ChatThread `json:"thread,omitempty"`
	Space        *ChatSpace  `json:"space,omitempty"`
	SlashCommand *SlashCmd   `json:"slashCommand,omitempty"`
	ArgumentText string      `json:"argumentText,omitempty"`
}

// ChatSpace is a Google Chat space.
type ChatSpace struct {
	Name        string `json:"name,omitempty"`
	Type        string `json:"type,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

// ChatUser represents a user in Google Chat.
type ChatUser struct {
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Type        string `json:"type,omitempty"`
}

// ChatThread represents a thread in Google Chat.
type ChatThread struct {
	Name string `json:"name,omitempty"`
}

// ChatAction represents an interactive card action.
type ChatAction struct {
	ActionMethodName string            `json:"actionMethodName,omitempty"`
	Parameters       []ActionParameter `json:"parameters,omitempty"`
}

// ActionParameter is a key-value parameter in card actions.
type ActionParameter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ChatCommon contains common event data.
type ChatCommon struct {
	InvokedFunction string `json:"invokedFunction,omitempty"`
}

// SlashCmd represents a slash command invocation.
type SlashCmd struct {
	CommandID int64 `json:"commandId,omitempty"`
}

// --- Card v2 types ---

// CardResponse is the response format for Google Chat cards.
type CardResponse struct {
	Text           string       `json:"text,omitempty"`
	CardsV2        []CardV2     `json:"cardsV2,omitempty"`
	Thread         *ChatThread  `json:"thread,omitempty"`
	ActionResponse *ActionResp  `json:"actionResponse,omitempty"`
}

// ActionResp controls how the response is displayed.
type ActionResp struct {
	Type string `json:"type,omitempty"` // NEW_MESSAGE, UPDATE_MESSAGE, DIALOG
}

// CardV2 is a Google Chat card v2.
type CardV2 struct {
	CardID string `json:"cardId,omitempty"`
	Card   Card   `json:"card"`
}

// Card is the card content.
type Card struct {
	Header   *CardHeader   `json:"header,omitempty"`
	Sections []CardSection `json:"sections,omitempty"`
}

// CardHeader is the card header with title and subtitle.
type CardHeader struct {
	Title     string `json:"title,omitempty"`
	Subtitle  string `json:"subtitle,omitempty"`
	ImageURL  string `json:"imageUrl,omitempty"`
	ImageType string `json:"imageType,omitempty"` // CIRCLE, SQUARE
}

// CardSection is a section in a card.
type CardSection struct {
	Header  string       `json:"header,omitempty"`
	Widgets []CardWidget `json:"widgets,omitempty"`
}

// CardWidget is a widget in a card section.
type CardWidget struct {
	TextParagraph *TextParagraph `json:"textParagraph,omitempty"`
	ButtonList    *ButtonList    `json:"buttonList,omitempty"`
	DecoratedText *DecoratedText `json:"decoratedText,omitempty"`
	Divider       *struct{}      `json:"divider,omitempty"`
}

// TextParagraph is a text widget.
type TextParagraph struct {
	Text string `json:"text"`
}

// ButtonList contains action buttons.
type ButtonList struct {
	Buttons []Button `json:"buttons"`
}

// Button is an interactive button.
type Button struct {
	Text    string      `json:"text"`
	OnClick ButtonClick `json:"onClick"`
	Color   *ButtonColor `json:"color,omitempty"`
}

// ButtonClick defines button click behavior.
type ButtonClick struct {
	Action   *ButtonAction `json:"action,omitempty"`
	OpenLink *OpenLink     `json:"openLink,omitempty"`
}

// ButtonAction defines a server-side action on click.
type ButtonAction struct {
	Function   string            `json:"function"`
	Parameters []ActionParameter `json:"parameters,omitempty"`
}

// OpenLink opens a URL.
type OpenLink struct {
	URL string `json:"url"`
}

// ButtonColor defines button color.
type ButtonColor struct {
	Red   float64 `json:"red"`
	Green float64 `json:"green"`
	Blue  float64 `json:"blue"`
}

// DecoratedText is a text widget with icon and label.
type DecoratedText struct {
	TopLabel string `json:"topLabel,omitempty"`
	Text     string `json:"text"`
	Icon     *Icon  `json:"startIcon,omitempty"`
}

// Icon is a material icon.
type Icon struct {
	KnownIcon string `json:"knownIcon,omitempty"`
}

func main() {
	var instanceName string
	var listenAddr string

	flag.StringVar(&instanceName, "instance", os.Getenv("INSTANCE_NAME"), "SympoziumInstance name")
	flag.StringVar(&listenAddr, "addr", ":3000", "Listen address for Google Chat webhook events")
	flag.Parse()

	log := zap.New(zap.UseDevMode(false)).WithName("channel-googlechat")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	projectID := os.Getenv("GCP_PROJECT_ID")

	// Initialize GCP auth
	tokenSrc, err := gcpauth.NewTokenSource(ctx,
		"https://www.googleapis.com/auth/chat.bot",
	)
	if err != nil {
		log.Error(err, "failed to initialize GCP auth")
		os.Exit(1)
	}

	// Initialize event bus (Cloud Pub/Sub)
	bus, err := eventbus.NewPubSubEventBus(projectID)
	if err != nil {
		log.Error(err, "failed to connect to event bus")
		os.Exit(1)
	}
	defer bus.Close()

	// Initialize telemetry
	tel, telErr := telemetry.Init(ctx, telemetry.Config{})
	if telErr != nil {
		log.Error(telErr, "telemetry init failed, continuing without")
	} else {
		defer tel.Shutdown(context.Background())
	}

	ch := &GoogleChatChannel{
		BaseChannel: channel.BaseChannel{
			ChannelType:  "googlechat",
			InstanceName: instanceName,
			EventBus:     bus,
		},
		log:       log,
		client:    &http.Client{Timeout: 30 * time.Second},
		tokenSrc:  gcpauth.NewCacheableTokenSource(tokenSrc, 5*time.Minute),
		projectID: projectID,
	}

	go ch.handleOutbound(ctx)

	log.Info("Starting Google Chat channel", "instance", instanceName, "addr", listenAddr,
		"auth", string(gcpauth.DetectAuthMethod()))

	ch.setHealthy(true)
	ch.runWebhookServer(ctx, listenAddr)
}

// runWebhookServer starts the HTTP server for Google Chat events.
func (gc *GoogleChatChannel) runWebhookServer(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/googlechat/events", gc.handleEvent)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		gc.mu.RLock()
		h := gc.healthy
		gc.mu.RUnlock()
		if h {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		gc.log.Error(err, "webhook server failed")
	}
}

// handleEvent processes all incoming Google Chat events.
func (gc *GoogleChatChannel) handleEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var event ChatEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	ctx, span := gchatTracer.Start(r.Context(), "googlechat.event."+strings.ToLower(event.Type),
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("sympozium.channel", "googlechat"),
			attribute.String("googlechat.event.type", event.Type),
		),
	)
	defer span.End()

	switch event.Type {
	case "MESSAGE":
		gc.handleMessage(ctx, w, &event)
	case "CARD_CLICKED":
		gc.handleCardAction(ctx, w, &event)
	case "ADDED_TO_SPACE":
		gc.handleAddedToSpace(w, &event)
	case "REMOVED_FROM_SPACE":
		gc.log.Info("Bot removed from space", "space", event.Space.Name)
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// handleMessage processes MESSAGE events, including slash commands.
func (gc *GoogleChatChannel) handleMessage(ctx context.Context, w http.ResponseWriter, event *ChatEvent) {
	msg := event.Message
	if msg == nil || msg.Sender == nil || msg.Sender.Type == "BOT" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Check for slash commands
	if msg.SlashCommand != nil {
		gc.handleSlashCommand(ctx, w, event)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Publish to event bus for agent processing
	inbound := channel.InboundMessage{
		SenderID:   msg.Sender.Name,
		SenderName: msg.Sender.DisplayName,
		ChatID:     event.Space.Name,
		Text:       text,
		Metadata: map[string]string{
			"messageName": msg.Name,
			"spaceType":   event.Space.Type,
		},
	}
	if msg.Thread != nil {
		inbound.ThreadID = msg.Thread.Name
	}

	if err := gc.PublishInbound(ctx, inbound); err != nil {
		gc.log.Error(err, "failed to publish inbound")
		gc.respondWithErrorCard(w, "Failed to process your message. Please try again.")
		return
	}

	// Send "thinking" card as immediate response
	gc.respondWithThinkingCard(w, msg.Sender.DisplayName)
}

// handleSlashCommand processes slash command invocations.
func (gc *GoogleChatChannel) handleSlashCommand(ctx context.Context, w http.ResponseWriter, event *ChatEvent) {
	msg := event.Message
	args := strings.TrimSpace(msg.ArgumentText)

	switch msg.SlashCommand.CommandID {
	case 1: // /ask — Ask the agent a question
		if args == "" {
			gc.respondWithCard(w, "Usage", "/ask <your question>", "DESCRIPTION", nil)
			return
		}
		inbound := channel.InboundMessage{
			SenderID:   msg.Sender.Name,
			SenderName: msg.Sender.DisplayName,
			ChatID:     event.Space.Name,
			Text:       args,
			Metadata:   map[string]string{"command": "ask"},
		}
		if msg.Thread != nil {
			inbound.ThreadID = msg.Thread.Name
		}
		_ = gc.PublishInbound(ctx, inbound)
		gc.respondWithThinkingCard(w, msg.Sender.DisplayName)

	case 2: // /status — Show agent status
		gc.respondWithStatusCard(w)

	case 3: // /run — Run a specific persona
		if args == "" {
			gc.respondWithCard(w, "Usage", "/run <persona-name> <task>", "DESCRIPTION", nil)
			return
		}
		parts := strings.SplitN(args, " ", 2)
		persona := parts[0]
		task := ""
		if len(parts) > 1 {
			task = parts[1]
		}
		inbound := channel.InboundMessage{
			SenderID:   msg.Sender.Name,
			SenderName: msg.Sender.DisplayName,
			ChatID:     event.Space.Name,
			Text:       task,
			Metadata: map[string]string{
				"command": "run",
				"persona": persona,
			},
		}
		_ = gc.PublishInbound(ctx, inbound)
		gc.respondWithCard(w, "Agent Dispatched",
			fmt.Sprintf("Running persona <b>%s</b> with task: %s", persona, task),
			"BOOKMARK", nil)

	case 4: // /help — Show help
		gc.respondWithHelpCard(w)

	default:
		gc.respondWithCard(w, "Unknown Command",
			"Use /help to see available commands.", "HELP", nil)
	}
}

// handleCardAction processes interactive card button clicks.
func (gc *GoogleChatChannel) handleCardAction(ctx context.Context, w http.ResponseWriter, event *ChatEvent) {
	if event.Action == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch event.Action.ActionMethodName {
	case "retry_task":
		for _, p := range event.Action.Parameters {
			if p.Key == "task" {
				inbound := channel.InboundMessage{
					SenderID:   event.User.Name,
					SenderName: event.User.DisplayName,
					ChatID:     event.Space.Name,
					Text:       p.Value,
					Metadata:   map[string]string{"command": "retry"},
				}
				_ = gc.PublishInbound(ctx, inbound)
			}
		}
		gc.respondWithCard(w, "Retrying", "Your task has been resubmitted.", "CLOCK", nil)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// handleAddedToSpace sends a welcome card when bot is added.
func (gc *GoogleChatChannel) handleAddedToSpace(w http.ResponseWriter, event *ChatEvent) {
	gc.log.Info("Bot added to space", "space", event.Space.Name, "type", event.Space.Type)

	resp := CardResponse{
		CardsV2: []CardV2{{
			CardID: "welcome",
			Card: Card{
				Header: &CardHeader{
					Title:    "Sympozium Agent Platform",
					Subtitle: "GCP-native AI agent orchestration",
				},
				Sections: []CardSection{
					{
						Widgets: []CardWidget{
							{TextParagraph: &TextParagraph{
								Text: "I'm your AI agent assistant, powered by <b>Gemini on Vertex AI</b> and running on <b>GKE</b>.\n\nI can help with infrastructure diagnosis, code review, and automated workflows.",
							}},
						},
					},
					{
						Header: "Quick Start",
						Widgets: []CardWidget{
							{DecoratedText: &DecoratedText{TopLabel: "Ask a question", Text: "<b>/ask</b> How do I scale my deployment?", Icon: &Icon{KnownIcon: "STAR"}}},
							{DecoratedText: &DecoratedText{TopLabel: "Check status", Text: "<b>/status</b>", Icon: &Icon{KnownIcon: "CLOCK"}}},
							{DecoratedText: &DecoratedText{TopLabel: "Run a persona", Text: "<b>/run</b> sre-agent check cluster health", Icon: &Icon{KnownIcon: "PERSON"}}},
							{DecoratedText: &DecoratedText{TopLabel: "Get help", Text: "<b>/help</b>", Icon: &Icon{KnownIcon: "HELP"}}},
						},
					},
				},
			},
		}},
	}

	writeJSON(w, resp)
}

// --- Response helpers ---

// respondWithThinkingCard sends a "processing" card as immediate response.
func (gc *GoogleChatChannel) respondWithThinkingCard(w http.ResponseWriter, userName string) {
	resp := CardResponse{
		CardsV2: []CardV2{{
			CardID: "thinking",
			Card: Card{
				Header: &CardHeader{
					Title:    "Processing...",
					Subtitle: fmt.Sprintf("Working on %s's request", userName),
				},
				Sections: []CardSection{{
					Widgets: []CardWidget{
						{TextParagraph: &TextParagraph{
							Text: "The agent is thinking. I'll reply in this thread when ready.",
						}},
					},
				}},
			},
		}},
	}
	writeJSON(w, resp)
}

// respondWithErrorCard sends an error card.
func (gc *GoogleChatChannel) respondWithErrorCard(w http.ResponseWriter, message string) {
	resp := CardResponse{
		CardsV2: []CardV2{{
			CardID: "error",
			Card: Card{
				Header: &CardHeader{Title: "Error"},
				Sections: []CardSection{{
					Widgets: []CardWidget{
						{DecoratedText: &DecoratedText{
							TopLabel: "Something went wrong",
							Text:     message,
							Icon:     &Icon{KnownIcon: "INVITE"},
						}},
					},
				}},
			},
		}},
	}
	writeJSON(w, resp)
}

// respondWithStatusCard shows agent platform status.
func (gc *GoogleChatChannel) respondWithStatusCard(w http.ResponseWriter) {
	authMethod := gcpauth.DetectAuthMethod()
	resp := CardResponse{
		CardsV2: []CardV2{{
			CardID: "status",
			Card: Card{
				Header: &CardHeader{
					Title:    "Sympozium Status",
					Subtitle: "GCP Agent Platform",
				},
				Sections: []CardSection{
					{
						Header: "System",
						Widgets: []CardWidget{
							{DecoratedText: &DecoratedText{TopLabel: "Instance", Text: gc.InstanceName, Icon: &Icon{KnownIcon: "MEMBERSHIP"}}},
							{DecoratedText: &DecoratedText{TopLabel: "Auth Method", Text: string(authMethod), Icon: &Icon{KnownIcon: "STAR"}}},
							{DecoratedText: &DecoratedText{TopLabel: "Event Bus", Text: "Cloud Pub/Sub", Icon: &Icon{KnownIcon: "CLOCK"}}},
							{DecoratedText: &DecoratedText{TopLabel: "LLM Provider", Text: "Vertex AI (Gemini)", Icon: &Icon{KnownIcon: "DESCRIPTION"}}},
						},
					},
				},
			},
		}},
	}
	writeJSON(w, resp)
}

// respondWithHelpCard shows available commands.
func (gc *GoogleChatChannel) respondWithHelpCard(w http.ResponseWriter) {
	resp := CardResponse{
		CardsV2: []CardV2{{
			CardID: "help",
			Card: Card{
				Header: &CardHeader{
					Title:    "Sympozium Commands",
					Subtitle: "Available slash commands",
				},
				Sections: []CardSection{
					{
						Widgets: []CardWidget{
							{DecoratedText: &DecoratedText{TopLabel: "/ask <question>", Text: "Ask the agent a question", Icon: &Icon{KnownIcon: "STAR"}}},
							{DecoratedText: &DecoratedText{TopLabel: "/status", Text: "Show agent platform status", Icon: &Icon{KnownIcon: "CLOCK"}}},
							{DecoratedText: &DecoratedText{TopLabel: "/run <persona> <task>", Text: "Run a specific persona with a task", Icon: &Icon{KnownIcon: "PERSON"}}},
							{DecoratedText: &DecoratedText{TopLabel: "/help", Text: "Show this help message", Icon: &Icon{KnownIcon: "HELP"}}},
						},
					},
					{
						Header: "Tips",
						Widgets: []CardWidget{
							{TextParagraph: &TextParagraph{
								Text: "You can also just type a message directly and the default agent will respond. In spaces, replies will be threaded automatically.",
							}},
						},
					},
				},
			},
		}},
	}
	writeJSON(w, resp)
}

// respondWithCard sends a simple card with optional buttons.
func (gc *GoogleChatChannel) respondWithCard(w http.ResponseWriter, title, body, icon string, buttons []Button) {
	widgets := []CardWidget{
		{TextParagraph: &TextParagraph{Text: body}},
	}
	if len(buttons) > 0 {
		widgets = append(widgets, CardWidget{ButtonList: &ButtonList{Buttons: buttons}})
	}

	resp := CardResponse{
		CardsV2: []CardV2{{
			CardID: "response",
			Card: Card{
				Header:   &CardHeader{Title: title},
				Sections: []CardSection{{Widgets: widgets}},
			},
		}},
	}
	writeJSON(w, resp)
}

// handleOutbound subscribes to outbound messages and sends them via Google Chat API.
func (gc *GoogleChatChannel) handleOutbound(ctx context.Context) {
	events, err := gc.SubscribeOutbound(ctx)
	if err != nil {
		gc.log.Error(err, "failed to subscribe to outbound")
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			var msg channel.OutboundMessage
			if err := json.Unmarshal(event.Data, &msg); err != nil {
				continue
			}
			if msg.Channel != "googlechat" {
				continue
			}
			if err := gc.sendOutboundCard(ctx, msg); err != nil {
				gc.log.Error(err, "failed to send outbound message")
			}
		}
	}
}

// sendOutboundCard sends an agent response as a rich card via the Chat API.
func (gc *GoogleChatChannel) sendOutboundCard(ctx context.Context, msg channel.OutboundMessage) error {
	// Build card response
	card := CardResponse{
		CardsV2: []CardV2{{
			CardID: "agent-response",
			Card: Card{
				Header: &CardHeader{
					Title:    "Agent Response",
					Subtitle: gc.InstanceName,
				},
				Sections: []CardSection{
					{
						Widgets: []CardWidget{
							{TextParagraph: &TextParagraph{Text: formatForChat(msg.Text)}},
						},
					},
					{
						Widgets: []CardWidget{
							{ButtonList: &ButtonList{Buttons: []Button{
								{
									Text: "Retry",
									OnClick: ButtonClick{Action: &ButtonAction{
										Function:   "retry_task",
										Parameters: []ActionParameter{{Key: "task", Value: msg.Text}},
									}},
								},
							}}},
						},
					},
				},
			},
		}},
	}

	if msg.ThreadID != "" {
		card.Thread = &ChatThread{Name: msg.ThreadID}
	}

	payload, _ := json.Marshal(card)

	url := fmt.Sprintf("https://chat.googleapis.com/v1/%s/messages", msg.ChatID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	// Add auth token
	token, err := gc.tokenSrc.GetAccessToken()
	if err != nil {
		return fmt.Errorf("getting access token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := gc.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Chat API error %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// formatForChat converts markdown-ish text to Google Chat format.
func formatForChat(text string) string {
	// Google Chat supports a subset of formatting:
	// *bold*, _italic_, ~strikethrough~, `code`, ```code block```
	// It also supports basic HTML: <b>, <i>, <a href>, <br>

	// Convert markdown code blocks to HTML
	text = strings.ReplaceAll(text, "```", "<code>")
	// Convert **bold** to <b>bold</b>
	// Simple approach — handle common patterns
	return text
}

// setHealthy updates health status and publishes to event bus.
func (gc *GoogleChatChannel) setHealthy(connected bool) {
	gc.mu.Lock()
	gc.healthy = connected
	gc.mu.Unlock()
	_ = gc.PublishHealth(context.Background(), channel.HealthStatus{Connected: connected})
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
