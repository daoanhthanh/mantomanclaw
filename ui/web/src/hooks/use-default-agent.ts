import { useCallback, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useHttp } from "@/hooks/use-ws";
import { useAuthStore } from "@/stores/use-auth-store";
import { queryKeys } from "@/lib/query-keys";
import { toast } from "@/stores/use-toast-store";
import i18n from "@/i18n";
import { userFriendlyError } from "@/lib/error-utils";
import { useAgents } from "@/pages/agents/hooks/use-agents";

/** Mirrors Go store.UserPreferences (tenant_users.metadata.preferences). */
interface UserPreferences {
  default_agent_id?: string | null;
}

/**
 * Resolves the agent a user starts chatting with:
 * saved user preference → tenant default (agents.is_default) → the only agent → none.
 * `setDefaultAgent` persists the user's own choice server-side (per user, per tenant).
 */
export function useDefaultAgent() {
  const http = useHttp();
  const connected = useAuthStore((s) => s.connected);
  const queryClient = useQueryClient();
  const { agents } = useAgents();

  const { data: prefs, isPending: prefsLoading } = useQuery({
    queryKey: queryKeys.me.preferences,
    queryFn: () => http.get<UserPreferences>("/v1/me/preferences"),
    staleTime: 60_000,
    enabled: connected,
  });

  /** agent_key of the resolved default, or "" when none can be determined. */
  const defaultAgentKey = useMemo(() => {
    const saved = agents.find((a) => a.id === prefs?.default_agent_id);
    if (saved) return saved.agent_key;
    const tenantDefault = agents.find((a) => a.is_default);
    if (tenantDefault) return tenantDefault.agent_key;
    return agents.length === 1 ? agents[0]!.agent_key : "";
  }, [agents, prefs?.default_agent_id]);

  const setDefaultAgent = useCallback(
    async (agentKey: string) => {
      const agent = agents.find((a) => a.agent_key === agentKey);
      if (!agent) return;
      const previous = queryClient.getQueryData<UserPreferences>(queryKeys.me.preferences);
      queryClient.setQueryData<UserPreferences>(queryKeys.me.preferences, { ...previous, default_agent_id: agent.id });
      try {
        const saved = await http.patch<UserPreferences>("/v1/me/preferences", { default_agent_id: agent.id });
        queryClient.setQueryData(queryKeys.me.preferences, saved);
        toast.success(i18n.t("agents:toast.defaultAgentSaved"), agent.display_name || agent.agent_key);
      } catch (err) {
        queryClient.setQueryData(queryKeys.me.preferences, previous);
        toast.error(i18n.t("agents:toast.defaultAgentSaveFailed"), userFriendlyError(err));
      }
    },
    [agents, http, queryClient],
  );

  return { defaultAgentKey, setDefaultAgent, loading: prefsLoading };
}
