<script lang="ts">
  import { lookup } from "../lookup";
  import type { Widget } from "../views";

  let { state, widget, view }: { state: unknown; widget: Widget; view: string } = $props();

  const rows = $derived((lookup(state, widget.from) as Record<string, unknown>[] | undefined) ?? []);

  function hrefFor(row: Record<string, unknown>): string {
    if (!widget.open) return "";
    const query = Object.entries(widget.open.param)
      .map(([param, field]) => `${encodeURIComponent(param)}=${encodeURIComponent(String(row[field] ?? ""))}`)
      .join("&");
    return `#/${view}/${widget.open.screen}?${query}`;
  }
</script>

<table>
  <thead>
    <tr>
      {#each widget.columns ?? [] as col}
        <th>{col}</th>
      {/each}
    </tr>
  </thead>
  <tbody>
    {#each rows as row}
      <tr>
        {#each widget.columns ?? [] as col, i}
          <td>
            {#if widget.open && i === 0}
              <a href={hrefFor(row)}>{row[col]}</a>
            {:else}
              {row[col]}
            {/if}
          </td>
        {/each}
      </tr>
    {/each}
  </tbody>
</table>
