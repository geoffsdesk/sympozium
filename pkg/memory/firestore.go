// Package memory provides persistent memory storage for Sympozium agents.
// It supports Firestore as a scalable, real-time document store that replaces
// the default Kubernetes ConfigMap-based memory system.
package memory

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// CollectionAgentMemory is the Firestore collection for agent memory.
	CollectionAgentMemory = "sympozium-agent-memory"

	// CollectionAgentTranscripts stores agent run transcripts.
	CollectionAgentTranscripts = "sympozium-agent-transcripts"
)

// Store defines the interface for agent memory persistence.
type Store interface {
	// GetMemory retrieves the current memory for an agent instance.
	GetMemory(ctx context.Context, instanceName string) (*AgentMemory, error)

	// UpdateMemory updates the memory content for an agent instance.
	UpdateMemory(ctx context.Context, instanceName string, content string) error

	// AppendTranscript stores a completed agent run transcript.
	AppendTranscript(ctx context.Context, instanceName string, transcript *Transcript) error

	// GetTranscripts retrieves recent transcripts for an agent instance.
	GetTranscripts(ctx context.Context, instanceName string, limit int) ([]*Transcript, error)

	// DeleteMemory deletes all memory for an agent instance.
	DeleteMemory(ctx context.Context, instanceName string) error

	// Close shuts down the store connection.
	Close() error
}

// AgentMemory represents the persistent memory state of an agent.
type AgentMemory struct {
	// InstanceName is the SympoziumInstance this memory belongs to.
	InstanceName string `firestore:"instanceName" json:"instanceName"`

	// Content is the markdown-formatted memory content.
	Content string `firestore:"content" json:"content"`

	// UpdatedAt is the last update timestamp.
	UpdatedAt time.Time `firestore:"updatedAt" json:"updatedAt"`

	// Version is incremented on each update for optimistic concurrency.
	Version int64 `firestore:"version" json:"version"`

	// TokenCount is the estimated token count of the memory content.
	TokenCount int `firestore:"tokenCount" json:"tokenCount"`
}

// Transcript records a single agent run execution.
type Transcript struct {
	// RunName is the AgentRun CR name.
	RunName string `firestore:"runName" json:"runName"`

	// InstanceName is the parent instance.
	InstanceName string `firestore:"instanceName" json:"instanceName"`

	// Task is the task that was executed.
	Task string `firestore:"task" json:"task"`

	// Response is the agent's final response.
	Response string `firestore:"response" json:"response"`

	// Model is the model used.
	Model string `firestore:"model" json:"model"`

	// ToolCalls records which tools were invoked.
	ToolCalls []ToolCall `firestore:"toolCalls" json:"toolCalls"`

	// StartedAt is when the run started.
	StartedAt time.Time `firestore:"startedAt" json:"startedAt"`

	// CompletedAt is when the run finished.
	CompletedAt time.Time `firestore:"completedAt" json:"completedAt"`

	// Duration is the total execution time.
	Duration time.Duration `firestore:"duration" json:"duration"`

	// TokensUsed is the total tokens consumed.
	TokensUsed int `firestore:"tokensUsed" json:"tokensUsed"`

	// Status is the run outcome (completed, failed, timed_out).
	Status string `firestore:"status" json:"status"`

	// Source is where the run was triggered from (channel, schedule, web, etc.).
	Source string `firestore:"source" json:"source"`
}

// ToolCall records a single tool invocation during a run.
type ToolCall struct {
	Name     string        `firestore:"name" json:"name"`
	Input    string        `firestore:"input" json:"input"`
	Output   string        `firestore:"output" json:"output"`
	Duration time.Duration `firestore:"duration" json:"duration"`
}

// FirestoreStore implements Store using Google Cloud Firestore.
type FirestoreStore struct {
	client    *firestore.Client
	projectID string
}

// NewFirestoreStore creates a new Firestore-backed memory store.
func NewFirestoreStore(ctx context.Context, projectID string) (*FirestoreStore, error) {
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("creating Firestore client: %w", err)
	}

	return &FirestoreStore{
		client:    client,
		projectID: projectID,
	}, nil
}

// GetMemory retrieves the current memory for an agent instance.
func (fs *FirestoreStore) GetMemory(ctx context.Context, instanceName string) (*AgentMemory, error) {
	doc, err := fs.client.Collection(CollectionAgentMemory).Doc(instanceName).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return &AgentMemory{
				InstanceName: instanceName,
				Content:      "",
				UpdatedAt:    time.Now(),
				Version:      0,
			}, nil
		}
		return nil, fmt.Errorf("getting memory for %s: %w", instanceName, err)
	}

	var mem AgentMemory
	if err := doc.DataTo(&mem); err != nil {
		return nil, fmt.Errorf("deserializing memory: %w", err)
	}
	return &mem, nil
}

// UpdateMemory updates the memory content with optimistic concurrency control.
func (fs *FirestoreStore) UpdateMemory(ctx context.Context, instanceName string, content string) error {
	ref := fs.client.Collection(CollectionAgentMemory).Doc(instanceName)

	return fs.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)

		var currentVersion int64
		if err == nil {
			var mem AgentMemory
			if err := doc.DataTo(&mem); err == nil {
				currentVersion = mem.Version
			}
		}

		return tx.Set(ref, AgentMemory{
			InstanceName: instanceName,
			Content:      content,
			UpdatedAt:    time.Now(),
			Version:      currentVersion + 1,
			TokenCount:   estimateTokens(content),
		})
	})
}

// AppendTranscript stores a completed agent run transcript.
func (fs *FirestoreStore) AppendTranscript(ctx context.Context, instanceName string, transcript *Transcript) error {
	transcript.InstanceName = instanceName

	_, _, err := fs.client.Collection(CollectionAgentTranscripts).Add(ctx, transcript)
	if err != nil {
		return fmt.Errorf("appending transcript for %s: %w", instanceName, err)
	}
	return nil
}

// GetTranscripts retrieves recent transcripts for an agent instance.
func (fs *FirestoreStore) GetTranscripts(ctx context.Context, instanceName string, limit int) ([]*Transcript, error) {
	if limit <= 0 {
		limit = 10
	}

	iter := fs.client.Collection(CollectionAgentTranscripts).
		Where("instanceName", "==", instanceName).
		OrderBy("completedAt", firestore.Desc).
		Limit(limit).
		Documents(ctx)

	var transcripts []*Transcript
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("iterating transcripts: %w", err)
		}

		var t Transcript
		if err := doc.DataTo(&t); err != nil {
			continue
		}
		transcripts = append(transcripts, &t)
	}

	return transcripts, nil
}

// DeleteMemory deletes all memory and transcripts for an agent instance.
func (fs *FirestoreStore) DeleteMemory(ctx context.Context, instanceName string) error {
	// Delete memory document
	_, err := fs.client.Collection(CollectionAgentMemory).Doc(instanceName).Delete(ctx)
	if err != nil && status.Code(err) != codes.NotFound {
		return fmt.Errorf("deleting memory: %w", err)
	}

	// Delete associated transcripts (batch)
	iter := fs.client.Collection(CollectionAgentTranscripts).
		Where("instanceName", "==", instanceName).
		Documents(ctx)

	batch := fs.client.Batch()
	count := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("iterating transcripts for delete: %w", err)
		}
		batch.Delete(doc.Ref)
		count++

		// Firestore batches are limited to 500 operations
		if count >= 400 {
			if _, err := batch.Commit(ctx); err != nil {
				return fmt.Errorf("batch delete: %w", err)
			}
			batch = fs.client.Batch()
			count = 0
		}
	}

	if count > 0 {
		if _, err := batch.Commit(ctx); err != nil {
			return fmt.Errorf("batch delete: %w", err)
		}
	}

	return nil
}

// Close shuts down the Firestore client.
func (fs *FirestoreStore) Close() error {
	return fs.client.Close()
}

// estimateTokens provides a rough token count estimate (4 chars ≈ 1 token).
func estimateTokens(content string) int {
	return len(content) / 4
}

// ConfigMapStore implements Store using Kubernetes ConfigMaps (legacy fallback).
// This preserves backward compatibility for clusters without Firestore.
type ConfigMapStore struct {
	// Implementation would use the existing ConfigMap-based logic
	// from the controller. Included here for interface completeness.
}
