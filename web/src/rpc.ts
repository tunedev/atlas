import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { UIService } from "./gen/atlas/web/v1/ui_pb";

export const ui = createClient(UIService, createConnectTransport({ baseUrl: location.origin }));

// exchange trades the one-time token in the URL fragment for the session
// cookie, then removes it from the address bar.
export async function exchange(): Promise<boolean> {
  const token = new URLSearchParams(location.hash.slice(1)).get("token");
  if (!token) return true;
  const res = await fetch("/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
  history.replaceState(null, "", location.pathname);
  return res.ok;
}
