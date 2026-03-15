// Package main is the entry point for the Sympozium IPC bridge sidecar.
// It runs inside agent pods and mediates between the agent container
// and the control plane via the event bus.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/alexsjones/sympozium/internal/eventbus"
	"github.com/alexsjones/sympozium/internal/ipc"
)

func main() {
	var basePath string
	var agentRunID string
	var instanceName string
	var gcpProjectID string

	flag.StringVar(&basePath, "ipc-path", "/ipc", "Base path for IPC directory")
	flag.StringVar(&agentRunID, "agent-run-id", os.Getenv("AGENT_RUN_ID"), "Agent run ID")
	flag.StringVar(&instanceName, "instance", os.Getenv("INSTANCE_NAME"), "SympoziumInstance name")
	flag.StringVar(&gcpProjectID, "gcp-project-id", os.Getenv("GCP_PROJECT_ID"), "GCP project ID for Pub/Sub event bus")
	flag.Parse()

	if agentRunID == "" {
		panic("AGENT_RUN_ID is required")
	}
	if gcpProjectID == "" {
		gcpProjectID = os.Getenv("GCP_PROJECT_ID")
	}

	log := zap.New(zap.UseDevMode(false)).WithName("ipc-bridge")

	// Connect to Pub/Sub event bus
	bus, err := eventbus.NewPubSubEventBus(gcpProjectID)
	if err != nil {
		log.Error(err, "failed to connect to event bus")
		os.Exit(1)
	}
	defer bus.Close()

	// Create and start bridge
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	bridge := ipc.NewBridge(basePath, agentRunID, instanceName, bus, log)
	if err := bridge.Start(ctx); err != nil {
		log.Error(err, "bridge failed")
		os.Exit(1)
	}
}
