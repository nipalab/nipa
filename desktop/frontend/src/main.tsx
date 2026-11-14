import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import { desktop } from "./api/desktop";
import { hydrateSettings } from "./lib/settings";
import { ThemeProvider } from "./lib/theme";
import "./index.css";

const HYDRATE_TIMEOUT_MS = 2000;

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false, staleTime: 30_000 } },
});

function loadUIState(): Promise<Record<string, string>> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("timed out")), HYDRATE_TIMEOUT_MS);
    desktop.uiState().then(
      (state) => {
        clearTimeout(timer);
        resolve(state);
      },
      (err: unknown) => {
        clearTimeout(timer);
        reject(err);
      },
    );
  });
}

function render() {
  ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
    <React.StrictMode>
      <QueryClientProvider client={queryClient}>
        <ThemeProvider>
          <App />
        </ThemeProvider>
      </QueryClientProvider>
    </React.StrictMode>,
  );
}

void hydrateSettings(loadUIState, desktop.saveUIState).then(render);
