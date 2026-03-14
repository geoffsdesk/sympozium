# Configuration

## Environment Variables

| Variable | Component | Description |
|----------|-----------|-------------|
| `EVENT_BUS_URL` | All | NATS server URL |
| `DATABASE_URL` | API Server | PostgreSQL connection string |
| `INSTANCE_NAME` | Channels | Owning SympoziumInstance name |
| `MEMORY_ENABLED` | Agent Runner | Whether persistent memory is active |
| `GOOGLE_CHAT_SERVICE_ACCOUNT` | Google Chat | Service account JSON credentials |

## LLM Providers

Sympozium is optimized for Google Cloud Vertex AI:

| Provider | Base URL | Credentials |
|----------|----------|-------------|
| Vertex AI (GCP) | (default) | GCP service account JSON or Application Default Credentials |

For GCP authentication, use one of:
- Service account JSON key file
- Workload Identity (recommended for GKE)
- Application Default Credentials (gcloud auth)
