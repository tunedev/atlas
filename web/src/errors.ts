// describeError renders any thrown value, including a ConnectError from a
// failed RPC, as plain text for a component's error state.
export function describeError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
