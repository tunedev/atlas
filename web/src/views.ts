// View is the JSON shape served by Views (views_json), one entry per loaded
// view file. It mirrors internal/adapters/inbound/web/view.go's json tags.

export type Open = {
  screen: string;
  param: Record<string, string>;
};

export type Widget = {
  kind: string;
  from: string;
  columns?: string[];
  keys?: string[];
  open?: Open;
};

export type Input = {
  name: string;
  options?: string[];
  text?: boolean;
};

export type Action = {
  label: string;
  input: Input[];
};

export type Screen = {
  id: string;
  title: string;
  params: string[];
  show: Widget[];
  actions: Action[];
};

export type View = {
  id: string;
  title: string;
  discloses: string[];
  screens: Screen[];
};
