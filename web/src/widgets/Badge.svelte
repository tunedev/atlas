<script lang="ts">
  import { lookup } from "../lookup";
  import type { Widget } from "../views";

  let { state, widget }: { state: unknown; widget: Widget } = $props();

  const value = $derived(lookup(state, widget.from));
</script>

<strong class="badge">{value}</strong>
