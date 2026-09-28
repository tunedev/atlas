<script lang="ts">
  import { lookup } from "../lookup";
  import type { Widget } from "../views";

  let { state, widget }: { state: unknown; widget: Widget } = $props();

  const obj = $derived((lookup(state, widget.from) as Record<string, unknown> | undefined) ?? {});
</script>

<dl>
  {#each widget.keys ?? [] as key}
    <dt>{key}</dt>
    <dd>{obj[key]}</dd>
  {/each}
</dl>
