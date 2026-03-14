FROM google/cloud-sdk:alpine

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN apk add --no-cache \
    bash \
    curl \
    git \
    jq \
    openssh-client \
    ca-certificates \
    tar \
    && true

# gcloud CLI is already included in the base image

# Create a non-root user for the sidecar.
RUN addgroup -g 1000 sympozium && \
    adduser -u 1000 -G sympozium -s /bin/bash -D sympozium && \
    mkdir -p /workspace /ipc/tools && \
    chown -R sympozium:sympozium /workspace /ipc

COPY images/skill-gcp-gitops/tool-executor.sh /usr/local/bin/tool-executor.sh
RUN chmod +x /usr/local/bin/tool-executor.sh

USER 1000

WORKDIR /workspace

CMD ["/usr/local/bin/tool-executor.sh"]
