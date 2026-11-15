import { createContext, useContext } from "react";
import { activeWindow, blockedNote, blocksSync, upcomingWindow, type MaintenanceWindow } from "@/lib/maintenance";

export type MaintenanceState = {
  active?: MaintenanceWindow;
  upcoming?: MaintenanceWindow;
  writes?: string;
  sync?: string;
};

export function maintenanceState(windows: MaintenanceWindow[]): MaintenanceState {
  const active = activeWindow(windows);
  const note = active ? blockedNote(active) : undefined;
  return { active, upcoming: upcomingWindow(windows), writes: note, sync: blocksSync(active) ? note : undefined };
}

const MaintenanceContext = createContext<MaintenanceState>({});

export const MaintenanceProvider = MaintenanceContext.Provider;

export const useMaintenance = (): MaintenanceState => useContext(MaintenanceContext);
