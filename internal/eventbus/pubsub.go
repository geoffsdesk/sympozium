package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"cloud.google.com/go/pubsub"
)

const (
	pubsubTopicPrefix = "sympozium-"
)

// PubSubEventBus implements EventBus using Google Cloud Pub/Sub.
type PubSubEventBus struct {
	client       *pubsub.Client
	projectID    string
	topics       map[string]*pubsub.Topic
	mu           sync.RWMutex
}

// NewPubSubEventBus creates a new Google Cloud Pub/Sub event bus.
func NewPubSubEventBus(projectID string) (*PubSubEventBus, error) {
	ctx := context.Background()
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("creating Pub/Sub client: %w", err)
	}

	return &PubSubEventBus{
		client:    client,
		projectID: projectID,
		topics:    make(map[string]*pubsub.Topic),
	}, nil
}

// getOrCreateTopic gets an existing topic or creates it.
func (p *PubSubEventBus) getOrCreateTopic(ctx context.Context, topic string) (*pubsub.Topic, error) {
	topicName := pubsubTopicPrefix + topic

	p.mu.RLock()
	if t, ok := p.topics[topicName]; ok {
		p.mu.RUnlock()
		return t, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if t, ok := p.topics[topicName]; ok {
		return t, nil
	}

	t := p.client.Topic(topicName)
	exists, err := t.Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking topic %s: %w", topicName, err)
	}

	if !exists {
		t, err = p.client.CreateTopic(ctx, topicName)
		if err != nil {
			return nil, fmt.Errorf("creating topic %s: %w", topicName, err)
		}
	}

	p.topics[topicName] = t
	return t, nil
}

// Publish sends an event to the Cloud Pub/Sub topic.
func (p *PubSubEventBus) Publish(ctx context.Context, topic string, event *Event) error {
	t, err := p.getOrCreateTopic(ctx, topic)
	if err != nil {
		return err
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshalling event: %w", err)
	}

	msg := &pubsub.Message{
		Data: data,
		Attributes: map[string]string{
			"topic": topic,
		},
	}

	// Inject trace context into attributes
	InjectTraceContext(ctx, natsHeaderAdapter(msg.Attributes))

	result := t.Publish(ctx, msg)
	_, err = result.Get(ctx)
	if err != nil {
		return fmt.Errorf("publishing to %s: %w", topic, err)
	}

	return nil
}

// natsHeaderAdapter wraps a map to satisfy the header interface expected by InjectTraceContext.
type natsHeaderAdapter map[string]string

func (h natsHeaderAdapter) Set(key, value string) {
	h[key] = value
}

func (h natsHeaderAdapter) Get(key string) string {
	return h[key]
}

func (h natsHeaderAdapter) Values(key string) []string {
	if v, ok := h[key]; ok {
		return []string{v}
	}
	return nil
}

// Subscribe returns a channel that receives events for the given topic.
func (p *PubSubEventBus) Subscribe(ctx context.Context, topic string) (<-chan *Event, error) {
	t, err := p.getOrCreateTopic(ctx, topic)
	if err != nil {
		return nil, err
	}

	// Create a unique subscription for this subscriber
	subName := fmt.Sprintf("%s%s-sub-%d", pubsubTopicPrefix, topic, time.Now().UnixNano())
	sub, err := p.client.CreateSubscription(ctx, subName, pubsub.SubscriptionConfig{
		Topic:            t,
		AckDeadline:      30 * time.Second,
		ExpirationPolicy: 24 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("creating subscription %s: %w", subName, err)
	}

	ch := make(chan *Event, 64)

	go func() {
		defer close(ch)
		err := sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
			var event Event
			if err := json.Unmarshal(msg.Data, &event); err != nil {
				msg.Nack()
				return
			}

			event.Ctx = ctx

			select {
			case ch <- &event:
				msg.Ack()
			case <-ctx.Done():
				msg.Nack()
			}
		})
		if err != nil && ctx.Err() == nil {
			fmt.Printf("Pub/Sub receive error: %v\n", err)
		}
	}()

	return ch, nil
}

// Close shuts down the Pub/Sub client.
func (p *PubSubEventBus) Close() error {
	// Stop all topic publish goroutines
	p.mu.Lock()
	for _, t := range p.topics {
		t.Stop()
	}
	p.mu.Unlock()

	return p.client.Close()
}
