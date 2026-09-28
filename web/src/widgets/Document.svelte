<script lang="ts">
  import { ui } from "../rpc";
  import { lookup } from "../lookup";
  import type { Widget } from "../views";

  let { state: recordState, widget }: { state: unknown; widget: Widget } = $props();

  const path = $derived((lookup(recordState, widget.from) as string | undefined) ?? "");
  let busy = $state(false);

  async function download() {
    if (!path) return;
    busy = true;
    try {
      const source = widget.kind === "file" ? "file" : "record";
      const res = await ui.document({ source, path });
      const blob = new Blob([res.body], { type: res.mediaType || "application/octet-stream" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = res.name || "download";
      a.click();
      URL.revokeObjectURL(url);
    } finally {
      busy = false;
    }
  }
</script>

{#if !path}
  <span>not drafted</span>
{:else}
  <button onclick={download} disabled={busy}>Download</button>
{/if}
