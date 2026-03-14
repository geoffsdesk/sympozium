# Channels

Channels connect Sympozium to external messaging platforms. Each channel runs as a dedicated Kubernetes Deployment. Messages flow through NATS JetStream and are routed to AgentRuns by the channel router.

## Supported Channels

| Channel | Protocol | Authentication | Status |
|---------|----------|-----------------|--------|
| **Google Chat** | Google Chat API | Service account or OAuth | **Stable** |

!!! info
    Sympozium supports Google Chat for integration with GCP-native environments.

Channels are optional. You can always interact through the TUI, web dashboard, or by creating AgentRun CRs directly with kubectl.

## Connecting Channels

Connect channels during onboarding or via the TUI edit modal:

| Channel | How to connect |
|---------|----------------|
| **Google Chat** | Create a service account in GCP, grant it Google Chat API permissions, and provide the service account JSON key during onboarding or set it in the SympoziumInstance channel config |

## Google Chat Setup

For Google Chat integration with GCP:

1. Create a service account in your GCP project
2. Grant the service account these roles:
   - `roles/chat.owner` (or custom role with necessary Chat API permissions)
3. Create a JSON key for the service account
4. Provide the JSON key during onboarding or in the channel configuration

The service account credentials are stored securely as a Kubernetes Secret and used to authenticate API calls to Google Chat.
