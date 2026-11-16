import type { DaemonClient } from "./client";
import type { Connection } from "./connector";

export type Connect = () => Promise<Connection>;

export class DaemonSession {
  private current?: Promise<Connection>;
  private listeners = new Set<(conn: Connection) => void>();

  constructor(private readonly connectFn: Connect) {}

  async client(): Promise<DaemonClient> {
    return (await this.connection()).client;
  }

  connection(): Promise<Connection> {
    if (!this.current) {
      const attempt = this.connectFn().then((conn) => {
        for (const listener of this.listeners) {
          listener(conn);
        }
        return conn;
      });
      attempt.catch(() => {
        if (this.current === attempt) {
          this.current = undefined;
        }
      });
      this.current = attempt;
    }
    return this.current;
  }

  reconnect(stale?: DaemonClient): Promise<Connection> {
    const current = this.current;
    if (current && stale) {
      return current.then(
        (conn) => {
          if (conn.client !== stale) {
            return conn;
          }
          this.current = undefined;
          return this.connection();
        },
        () => this.connection(),
      );
    }
    this.current = undefined;
    return this.connection();
  }

  onConnected(listener: (conn: Connection) => void): { dispose(): void } {
    this.listeners.add(listener);
    return { dispose: () => this.listeners.delete(listener) };
  }
}
