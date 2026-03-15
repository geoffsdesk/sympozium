import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

// ── Static fallback model lists ──────────────────────────────────────────────

const VERTEXAI_MODELS = [
  "gemini-2.5-pro",
  "gemini-2.5-flash",
  "gemini-3.1-pro-preview",
  "gemini-2.0-flash",
  "gemini-1.5-pro",
];

const GEMMA_MODELS = [
  "gemma-3-27b-it",
  "gemma-3-12b-it",
  "gemma-3-4b-it",
  "gemma-3-1b-it",
];

// ── Fetchers ─────────────────────────────────────────────────────────────────

async function fetchVertexAIModels(apiKey: string): Promise<string[]> {
  // Note: Full dynamic model listing from Vertex AI would require server-side auth.
  // For now, return a static list of common Gemini models.
  // In production, implement server-side fetching with proper GCP credentials.
  return VERTEXAI_MODELS;
}

async function fetchProviderModelsViaProxy(baseURL: string): Promise<string[]> {
  const res = await api.providers.models(baseURL);
  return res.models;
}

// ── Hook ─────────────────────────────────────────────────────────────────────

/**
 * Fetches the model list for a given provider + API key.
 * For local providers (ollama, custom) with a baseURL, proxies through the backend.
 * Falls back to a curated static list if the API call fails or no key is given.
 */
export function useModelList(provider: string, apiKey: string, baseURL?: string) {
  const isLocalProvider = provider === "ollama" || provider === "custom";
  const canFetchLocal = isLocalProvider && !!baseURL;
  const canFetchCloud = !!apiKey && provider === "vertexai";

  const query = useQuery<string[]>({
    queryKey: ["provider-models", provider, apiKey, baseURL],
    queryFn: async () => {
      if (canFetchLocal) return fetchProviderModelsViaProxy(baseURL!);
      if (provider === "vertexai" && apiKey) return fetchVertexAIModels(apiKey);
      throw new Error("no-fetch");
    },
    enabled: canFetchLocal || canFetchCloud,
    staleTime: 5 * 60 * 1000, // cache 5 min
    retry: false,
  });

  // Static fallback when fetch isn't available or failed
  const fallback = (() => {
    switch (provider) {
      case "vertexai":
        return VERTEXAI_MODELS;
      case "ollama":
        return GEMMA_MODELS;
      default:
        return [];
    }
  })();

  return {
    models: query.data ?? fallback,
    isLoading: query.isLoading && query.fetchStatus !== "idle",
    isLive: !!query.data, // true if we got real data from the API
  };
}
