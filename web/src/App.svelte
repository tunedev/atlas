<script lang="ts">
  import { Code, ConnectError } from "@connectrpc/connect";
  import { exchange, ui } from "./rpc";
  import { describeError } from "./errors";
  import Screen from "./Screen.svelte";
  import type { View } from "./views";

  type Route = { view: string; screen: string; params: Record<string, string> };

  let views: { viewsJson: string; egress: { endpoint: string; hosted: boolean; tools: string[]; acknowledged: boolean }[] } | null = $state(null);
  let authFailed = $state(false);
  let viewsError = $state<string | null>(null);
  let hash = $state(location.hash);
  let egressOpen = $state(false);

  window.addEventListener("hashchange", () => {
    hash = location.hash;
  });

  $effect(() => {
    (async () => {
      const ok = await exchange();
      if (!ok) {
        authFailed = true;
        return;
      }
      try {
        views = await ui.views({});
      } catch (err) {
        if (ConnectError.from(err).code === Code.Unauthenticated) authFailed = true;
        else viewsError = describeError(err);
      }
    })();
  });

  const parsedViews = $derived(views ? (JSON.parse(views.viewsJson) as View[]) : []);
  const hostedEndpoints = $derived(views ? views.egress.filter((e) => e.hosted) : []);

  function parseHash(h: string): Route {
    const body = h.startsWith("#/") ? h.slice(2) : "";
    const [path, query] = body.split("?");
    const parts = path.split("/").filter(Boolean);
    const params: Record<string, string> = {};
    if (query) {
      for (const [k, v] of new URLSearchParams(query)) params[k] = v;
    }
    return { view: parts[0] ?? "", screen: parts[1] ?? "", params };
  }

  const route = $derived(parseHash(hash));
  const currentView = $derived(parsedViews.find((v) => v.id === route.view));
  const currentScreen = $derived(currentView?.screens.find((s) => s.id === route.screen));
</script>

<header>
  <button class="egress" onclick={() => (egressOpen = !egressOpen)}>
    {#if hostedEndpoints.length > 0}
      Data leaves this machine: {hostedEndpoints.map((e) => e.endpoint).join(", ")}
    {:else}
      Everything stays on this machine
    {/if}
  </button>
  {#if egressOpen && views}
    <table>
      <thead>
        <tr>
          <th>Endpoint</th>
          <th>Hosted</th>
          <th>Tools</th>
          <th>Acknowledged</th>
        </tr>
      </thead>
      <tbody>
        {#each views.egress as e}
          <tr>
            <td>{e.endpoint}</td>
            <td>{e.hosted ? "yes" : "no"}</td>
            <td>{e.tools.join(", ")}</td>
            <td>{e.acknowledged ? "yes" : "no"}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</header>

<main>
  {#if authFailed}
    <p>Open the link atlas printed when it started</p>
  {:else if viewsError}
    <p class="error">{viewsError}</p>
  {:else if !views}
    <p>Loading...</p>
  {:else if currentView && currentScreen}
    {#key hash}
      <Screen view={currentView.id} screen={currentScreen} params={route.params} />
    {/key}
  {:else}
    {#each parsedViews as v}
      <section>
        <h2>{v.title}</h2>
        <ul>
          {#each v.screens as s}
            <li><a href="#/{v.id}/{s.id}">{s.title}</a></li>
          {/each}
        </ul>
      </section>
    {/each}
  {/if}
</main>
