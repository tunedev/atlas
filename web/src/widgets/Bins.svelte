<script lang="ts">
  import { lookup } from "../lookup";
  import type { Widget } from "../views";

  type Bin = { reads: string; n: number; mean_predicted: number; observed_rate: number | null };

  let { state, widget }: { state: unknown; widget: Widget } = $props();

  const bins = $derived((lookup(state, widget.from) as Bin[] | undefined) ?? []);

  // Bar widths are CSS classes in 5% steps, not inline styles: the page CSP
  // allows no inline style attributes.
  function widthClass(value: number): string {
    const pct = Math.max(0, Math.min(100, Math.round(value * 100)));
    const step = Math.round(pct / 5) * 5;
    return `bar-${step}`;
  }
</script>

<table>
  <thead>
    <tr>
      <th>Reads</th>
      <th>n</th>
      <th>Predicted</th>
      <th>Observed</th>
    </tr>
  </thead>
  <tbody>
    {#each bins as bin}
      <tr>
        <td>{bin.reads}</td>
        <td>{bin.n}</td>
        <td>
          <span class="bar-track"><span class="bar-fill {widthClass(bin.mean_predicted)}"></span></span>
        </td>
        <td>
          {#if bin.observed_rate === null}
            too few to say (n={bin.n})
          {:else}
            <span class="bar-track"><span class="bar-fill {widthClass(bin.observed_rate)}"></span></span>
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>
