<script lang="ts">
  import { lookup } from "../lookup";
  import type { Widget } from "../views";

  let { state, widget }: { state: unknown; widget: Widget } = $props();

  const items = $derived((lookup(state, widget.from) as unknown[] | undefined) ?? []);
</script>

<ul>
  {#each items as item}
    <li>{item}</li>
  {/each}
</ul>
