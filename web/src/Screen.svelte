<script lang="ts">
  import { ui } from "./rpc";
  import { describeError } from "./errors";
  import type { Screen as ScreenDef } from "./views";
  import Table from "./widgets/Table.svelte";
  import Fields from "./widgets/Fields.svelte";
  import Badge from "./widgets/Badge.svelte";
  import ListWidget from "./widgets/List.svelte";
  import Text from "./widgets/Text.svelte";
  import Bins from "./widgets/Bins.svelte";
  import Document from "./widgets/Document.svelte";
  import Ask from "./Ask.svelte";
  import Consent from "./Consent.svelte";

  let { view, screen, params }: {
    view: string;
    screen: ScreenDef;
    params: Record<string, string>;
  } = $props();

  type StepEvent = { stepId: string; tool: string; status: string; error: string };
  type AskEvent = { id: string; toolName: string; kind: string; summary: string };
  type ConsentEvent = { endpoints: string[]; discloses: string[] };

  let queued = $state<number | null>(null);
  let steps = $state<StepEvent[]>([]);
  let ask = $state<AskEvent | null>(null);
  let consent = $state<ConsentEvent | null>(null);
  let screenState = $state<unknown>(null);
  let running = $state(false);
  let openForm = $state<number | null>(null);
  let formValues = $state<Record<string, string>>({});
  let error = $state<string | null>(null);

  let pendingAction = -1;
  let pendingInputs: Record<string, string> = {};
  let controller: AbortController | null = null;

  $effect(() => {
    run(-1, {});
    return () => controller?.abort();
  });

  async function run(action: number, inputs: Record<string, string>) {
    const ac = new AbortController();
    controller = ac;
    running = true;
    queued = null;
    steps = [];
    ask = null;
    consent = null;
    error = null;
    pendingAction = action;
    pendingInputs = inputs;
    try {
      for await (const ev of ui.run({ view, screen: screen.id, action, params, inputs }, { signal: ac.signal })) {
        const event = ev.event;
        if (event.case === "queued") {
          queued = event.value.ahead;
        } else if (event.case === "step") {
          steps = [...steps, event.value];
        } else if (event.case === "ask") {
          ask = event.value;
        } else if (event.case === "needsAcknowledgement") {
          consent = event.value;
        } else if (event.case === "done") {
          if (action === -1) {
            screenState = JSON.parse(event.value.stateJson);
          } else {
            openForm = null;
            await run(-1, {});
          }
        }
      }
    } catch (err) {
      if (!ac.signal.aborted) error = describeError(err);
    } finally {
      running = false;
    }
  }

  async function onAllow() {
    if (!ask) return;
    try {
      await ui.answer({ id: ask.id, allow: true });
    } catch (err) {
      error = describeError(err);
    } finally {
      ask = null;
    }
  }

  async function onDeny() {
    if (!ask) return;
    try {
      await ui.answer({ id: ask.id, allow: false });
    } catch (err) {
      error = describeError(err);
    } finally {
      ask = null;
    }
  }

  async function onAcknowledge() {
    if (!consent) return;
    try {
      for (const endpoint of consent.endpoints) {
        await ui.acknowledge({ endpoint });
      }
      consent = null;
      await run(pendingAction, pendingInputs);
    } catch (err) {
      error = describeError(err);
    }
  }

  function onCancelConsent() {
    consent = null;
  }

  function runAction(index: number) {
    const action = screen.actions[index];
    if (action.input.length > 0) {
      const values: Record<string, string> = {};
      for (const input of action.input) values[input.name] = input.options?.[0] ?? "";
      formValues = values;
      openForm = index;
      return;
    }
    run(index, {});
  }

  function submitForm(index: number) {
    run(index, { ...formValues });
  }
</script>

<h1>{screen.title}</h1>

{#if error}
  <p class="error">{error}</p>
{/if}

{#if queued !== null}
  <p>waiting ({queued} ahead)</p>
{/if}

{#if steps.length > 0}
  <ul>
    {#each steps as s}
      <li>{s.stepId} {s.tool} {s.status}{#if s.error} - {s.error}{/if}</li>
    {/each}
  </ul>
{/if}

{#if ask}
  <Ask {ask} onAllow={onAllow} onDeny={onDeny} />
{/if}

{#if consent}
  <Consent {consent} onAcknowledge={onAcknowledge} onCancel={onCancelConsent} />
{/if}

{#if screenState}
  {#each screen.show as widget}
    {#if widget.kind === "table"}
      <Table state={screenState} {widget} {view} />
    {:else if widget.kind === "fields"}
      <Fields state={screenState} {widget} />
    {:else if widget.kind === "badge"}
      <Badge state={screenState} {widget} />
    {:else if widget.kind === "list"}
      <ListWidget state={screenState} {widget} />
    {:else if widget.kind === "text"}
      <Text state={screenState} {widget} />
    {:else if widget.kind === "bins"}
      <Bins state={screenState} {widget} />
    {:else if widget.kind === "document" || widget.kind === "file"}
      <Document state={screenState} {widget} />
    {/if}
  {/each}
{/if}

{#each screen.actions as action, i}
  <div>
    <button onclick={() => runAction(i)} disabled={running}>{action.label}</button>
    {#if openForm === i}
      <div class="form">
        {#each action.input as input}
          <label>
            {input.name}
            {#if input.options}
              <select bind:value={formValues[input.name]}>
                {#each input.options as opt}
                  <option value={opt}>{opt}</option>
                {/each}
              </select>
            {:else}
              <textarea bind:value={formValues[input.name]}></textarea>
            {/if}
          </label>
        {/each}
        <button onclick={() => submitForm(i)} disabled={running}>Submit</button>
      </div>
    {/if}
  </div>
{/each}
