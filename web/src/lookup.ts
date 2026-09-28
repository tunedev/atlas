// lookup walks a dotted path ("a.b.c") through a parsed JSON value. Any
// missing or non-object step along the way yields undefined.
export function lookup(state: unknown, path: string): unknown {
  if (state == null || path === "") return undefined;
  return path.split(".").reduce<unknown>((acc, key) => {
    if (acc == null || typeof acc !== "object") return undefined;
    return (acc as Record<string, unknown>)[key];
  }, state);
}
