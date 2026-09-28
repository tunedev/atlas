<script lang="ts">
  let { consent, onAcknowledge, onCancel }: {
    consent: { endpoints: string[]; discloses: string[] };
    onAcknowledge: () => void;
    onCancel: () => void;
  } = $props();
</script>

<div class="modal">
  <p>
    This will send <strong>{consent.discloses.join(", ")}</strong> to
    <strong>{consent.endpoints.join(", ")}</strong>, which is not on this
    machine. Nothing is sent until you agree.
  </p>
  <button onclick={onAcknowledge}>Send and remember for this endpoint</button>
  <button onclick={onCancel}>Cancel</button>
</div>
