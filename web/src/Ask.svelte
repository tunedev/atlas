<script lang="ts">
  let { ask, onAllow, onDeny }: {
    ask: { id: string; toolName: string; kind: string; summary: string };
    onAllow: () => void;
    onDeny: () => void;
  } = $props();
</script>

<div class="modal">
  <p>{ask.summary}</p>
  <p>{ask.toolName} ({ask.kind})</p>
  <button onclick={onAllow}>Allow</button>
  <button onclick={onDeny}>Deny</button>
</div>
