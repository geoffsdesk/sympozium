// Package main is the entry point for the Google Chat channel pod.
// It uses the Google Chat API to receive and send messages.
// The pod uses a service account for authentication with the Google Chat API.
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

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/alexsjones/sympozium/internal/channel"
	"github.com/alexsjones/sympozium/internal/eventbus"
	"github.com/alexsjones/sympozium/pkg/telemetry"
)

var gchatTracer = otel.Tracer("sympozium.ai/channel-googlechat")

// GoogleChatChannel implements the Google Chat channel using the Chat API.
type GoogleChatChannel struct {
	channel.BaseChannel
	ServiceAccountKeyPath string
	ProjectID            string
	log                  logr.Logger
	client               *http.Client
	healthy              bool
	mu                   sync.RWMutex
}

func main() {
	var instanceName string
	var eventBusURL string
	var listenAddr string

	flag.StringVar(&instanceName, "instance", os.Getenv("INSTANCE_NAME"), "SympoziumInstance name")
	flag.StringVar(&eventBusURL, "event-bus-url", os.Getenv("EVENT_BUS_URL"), "Event bus URL")
	flag.StringVar(&listenAddr, "addr", ":3000", "Listen address for Google Chat webhook events")
	flag.Parse()

	log := zap.New(zap.UseDevMode(false)).WithName("channel-googlechat")

	bus, err := eventbus.NewPubSubEventBus(os.Getenv("GCP_PROJECT_ID"))
	if err != nil {
		log.Error(err, "failed to connect to event bus")
		os.Exit(1)
	}
	defer bus.Close()

	ch := &GoogleChatChannel{
		BaseChannel: channel.BaseChannel{
			ChannelType:  "googlechat",
			InstanceName: instanceName,
			EventBus:     bus,
		},
		ServiceAccountKeyPath: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		ProjectID:            os.Getenv("GCP_PROJECT_ID"),
		log:                  log,
		client:               &http.Client{Timeout: 30 * time.Second},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Initialize OpenTelemetry.
	tel, telErr := telemetry.Init(ctx, telemetry.Config{})
	if telErr != nil {
		log.Error(telErr, "failed to init telemetry, continuing without")
	} else {
		defer tel.Shutdown(context.Background())
	}

	// Health server
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			ch.mu.RLock()
			h := ch.healthy
			ch.mu.RUnlock()
			if h {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		})
		_ = http.ListenAndServe(":8080", mux)
	}()

	go ch.handleOutbound(ctx)

	log.Info("Starting Google Chat channel in webhook mode", "instance", instanceName, "addr", listenAddr)
	ch.runWebhook(ctx, listenAddr)
}

// runWebhook starts an HTTP server to receive Google Chat webhook events.
func (gc *GoogleChatChannel) runWebhook(ctx context.Context, addr string) {
	gc.setHealthy(true, "")

	mux := http.NewServeMux()
	mux.HandleFunc("/googlechat/events", gc.handleChatEvents)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		gc.mu.RLock()
		h := gc.healthy
		gc.mu.RUnlock()
		if h {
			w.WriteHeader(http.StatusOK)
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
		gc.log.Error(err, "google chat events server failed")
	}
}

// handleChatEvents processes incoming Google Chat webhook payloads.
func (gc *GoogleChatChannel) handleChatEvents(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var event struct {
		Type    string `json:"type"`
		Message struct {
			Name   string `json:"name"`
			Sender struct {
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
				Type        string `json:"type"`
			} `json:"sender"`
			Text   string `json:"text"`
			Thread struct {
				Name string `json:"name"`
			} `json:"thread"`
			Space struct {
				Name string `json:"name"`
			} `json:"space"`
			CreateTime string `json:"createTime"`
		} `json:"message"`
		Space struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"space"`
	}

	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	switch event.Type {
	case "MESSAGE":
		// Ignore bot messages
		if event.Message.Sender.Type == "BOT" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Start trace span
		ctx, span := gchatTracer.Start(r.Context(), "googlechat.message.received",
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("sympozium.channel", "googlechat"),
				attribute.String("sympozium.sender.id", event.Message.Sender.Name),
				attribute.String("messaging.system", "googlechat"),
				attribute.String("messaging.destination.name", event.Space.Name),
			),
		)
		defer span.End()

		msg := channel.InboundMessage{
			SenderID:   event.Message.Sender.Name,
			SenderName: event.Message.Sender.DisplayName,
			ChatID:     event.Space.Name,
			ThreadID:   event.Message.Thread.Name,
			Text:       event.Message.Text,
			Metadata: map[string]string{
				"messageName": event.Message.Name,
				"spaceType":   event.Space.Type,
			},
		}

		if err := gc.PublishInbound(ctx, msg); err != nil {
			span.RecordError(err)
			gc.log.Error(err, "failed to publish inbound from Google Chat")
		}

	case "ADDED_TO_SPACE":
		gc.log.Info("Bot added to space", "space", event.Space.Name)

	case "REMOVED_FROM_SPACE":
		gc.log.Info("Bot removed from space", "space", event.Space.Name)
	}

	w.WriteHeader(http.StatusOK)
}

// handleOutbound subscribes to outbound messages and sends them via Google Chat API.
func (gc *GoogleChatChannel) handleOutbound(ctx context.Context) {
	events, err := gc.SubscribeOutbound(ctx)
	if err != nil {
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
			_ = gc.sendMessage(ctx, msg)
		}
	}
}

// sendMessage sends a message via the Google Chat API.
func (gc *GoogleChatChannel) sendMessage(ctx context.Context, msg channel.OutboundMessage) error {
	// Use Google Chat API to send message
	url := fmt.Sprintf("https://chat.googleapis.com/v1/%s/messages", msg.ChatID)

	payload := map[string]interface{}{
		"text": msg.Text,
	}
	if msg.ThreadID != "" {
		payload["thread"] = map[string]string{"name": msg.ThreadID}
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Note: In production, use OAuth2 token from service account
	// The token would be obtained via Google Cloud metadata or GOOGLE_APPLICATION_CREDENTIALS

	resp, err := gc.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// setHealthy updates the health status and publishes it to the event bus.
func (gc *GoogleChatChannel) setHealthy(connected bool, message string) {
	gc.mu.Lock()
	gc.healthy = connected
	gc.mu.Unlock()
	_ = gc.PublishHealth(context.Background(), channel.HealthStatus{
		Connected: connected,
		Message:   message,
	})
}
