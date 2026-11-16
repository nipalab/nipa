import * as path from "node:path";

type Listener<T> = (value: T) => void;

export class EventEmitter<T> {
  private listeners = new Set<Listener<T>>();
  readonly event = (listener: Listener<T>) => {
    this.listeners.add(listener);
    return { dispose: () => this.listeners.delete(listener) };
  };
  fire(value: T): void {
    for (const l of this.listeners) {
      l(value);
    }
  }
  dispose(): void {
    this.listeners.clear();
  }
}

export class Uri {
  private constructor(
    readonly scheme: string,
    readonly path: string,
    readonly query: string,
  ) {}
  static file(p: string): Uri {
    return new Uri("file", path.resolve(p), "");
  }
  static from(parts: { scheme: string; path?: string; query?: string }): Uri {
    return new Uri(parts.scheme, parts.path ?? "", parts.query ?? "");
  }
  get fsPath(): string {
    return this.path;
  }
  toString(): string {
    return `${this.scheme}://${this.path}${this.query ? `?${this.query}` : ""}`;
  }
}

export class ThemeColor {
  constructor(readonly id: string) {}
}

export class FileDecoration {
  propagate?: boolean;
  constructor(
    readonly badge?: string,
    readonly tooltip?: string,
    readonly color?: ThemeColor,
  ) {}
}

export enum ProgressLocation {
  SourceControl = 1,
  Window = 10,
  Notification = 15,
}

export class CancellationTokenSource {
  private emitter = new EventEmitter<void>();
  isCancellationRequested = false;
  readonly token = {
    isCancellationRequested: false,
    onCancellationRequested: this.emitter.event,
  };
  cancel(): void {
    this.token.isCancellationRequested = true;
    this.emitter.fire();
  }
}

export interface FakeGroup {
  id: string;
  label: string;
  hideWhenEmpty?: boolean;
  resourceStates: any[];
  dispose(): void;
}

export interface FakeSourceControl {
  id: string;
  label: string;
  rootUri: Uri;
  inputBox: { value: string; placeholder: string; enabled: boolean; visible: boolean };
  count?: number;
  quickDiffProvider?: any;
  acceptInputCommand?: any;
  statusBarCommands?: any[];
  groups: FakeGroup[];
  disposed: boolean;
  createResourceGroup(id: string, label: string): FakeGroup;
  dispose(): void;
}

type Answer = string | undefined | ((message: string, ...rest: any[]) => string | undefined);

export const fake = {
  sourceControls: [] as FakeSourceControl[],
  messages: [] as { level: "info" | "warning" | "error"; text: string; options?: any }[],
  answers: [] as Answer[],
  quickPick: undefined as ((items: any[]) => any) | undefined,
  commands: new Map<string, (...args: any[]) => any>(),
  executed: [] as { command: string; args: any[] }[],
  config: {} as Record<string, unknown>,
  contentProviders: new Map<string, any>(),
  decorationProviders: [] as any[],
  folderListeners: [] as (() => void)[],
  workspaceFolders: undefined as { uri: Uri }[] | undefined,
  foundFiles: [] as Uri[],
  output: [] as string[],
  outputShown: 0,
  activeFile: undefined as string | undefined,
  cancelProgress: false,
  reset(): void {
    this.sourceControls = [];
    this.messages = [];
    this.answers = [];
    this.quickPick = undefined;
    this.commands = new Map();
    this.executed = [];
    this.config = {};
    this.contentProviders = new Map();
    this.decorationProviders = [];
    this.folderListeners = [];
    this.workspaceFolders = undefined;
    this.foundFiles = [];
    this.output = [];
    this.outputShown = 0;
    this.activeFile = undefined;
    this.cancelProgress = false;
  },
};

function answer(level: "info" | "warning" | "error", text: string, rest: any[]): Promise<string | undefined> {
  const options = rest.length && typeof rest[0] === "object" ? rest[0] : undefined;
  fake.messages.push({ level, text, options });
  const next = fake.answers.shift();
  return Promise.resolve(typeof next === "function" ? next(text, ...rest) : next);
}

export const scm = {
  createSourceControl(id: string, label: string, rootUri: Uri): FakeSourceControl {
    const sc: FakeSourceControl = {
      id,
      label,
      rootUri,
      inputBox: { value: "", placeholder: "", enabled: true, visible: true },
      groups: [],
      disposed: false,
      createResourceGroup(gid: string, glabel: string) {
        const group: FakeGroup = { id: gid, label: glabel, resourceStates: [], dispose() {} };
        sc.groups.push(group);
        return group;
      },
      dispose() {
        sc.disposed = true;
      },
    };
    fake.sourceControls.push(sc);
    return sc;
  },
};

export const window = {
  showInformationMessage: (text: string, ...rest: any[]) => answer("info", text, rest),
  showWarningMessage: (text: string, ...rest: any[]) => answer("warning", text, rest),
  showErrorMessage: (text: string, ...rest: any[]) => answer("error", text, rest),
  showQuickPick: async (items: any[]) => fake.quickPick?.(items),
  async withProgress<R>(_opts: any, task: (progress: any, token: any) => Promise<R>): Promise<R> {
    const source = new CancellationTokenSource();
    const reports: any[] = [];
    const promise = task({ report: (r: any) => reports.push(r) }, source.token);
    if (fake.cancelProgress) {
      source.cancel();
    }
    return promise;
  },
  createOutputChannel: (_name: string) => ({
    appendLine: (line: string) => fake.output.push(line),
    show: () => {
      fake.outputShown++;
    },
    dispose() {},
  }),
  registerFileDecorationProvider: (provider: any) => {
    fake.decorationProviders.push(provider);
    return { dispose() {} };
  },
  get activeTextEditor() {
    return fake.activeFile ? { document: { uri: Uri.file(fake.activeFile) } } : undefined;
  },
};

export const commands = {
  registerCommand(id: string, fn: (...args: any[]) => any) {
    fake.commands.set(id, fn);
    return { dispose: () => fake.commands.delete(id) };
  },
  async executeCommand(command: string, ...args: any[]) {
    fake.executed.push({ command, args });
    return fake.commands.get(command)?.(...args);
  },
};

export const workspace = {
  getConfiguration: (_section: string) => ({
    get: <T>(key: string) => fake.config[key] as T | undefined,
  }),
  registerTextDocumentContentProvider(scheme: string, provider: any) {
    fake.contentProviders.set(scheme, provider);
    return { dispose() {} };
  },
  onDidChangeWorkspaceFolders(listener: () => void) {
    fake.folderListeners.push(listener);
    return { dispose() {} };
  },
  findFiles: async () => fake.foundFiles,
  get workspaceFolders() {
    return fake.workspaceFolders;
  },
};
