import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { MaintenanceBanner } from "./MaintenanceBanner";
import { maintenanceState } from "@/hooks/useMaintenance";
import { formatLocal, type MaintenanceWindow } from "@/lib/maintenance";
import { renderWithProviders } from "@/test/render";

const w = (over: Partial<MaintenanceWindow> = {}): MaintenanceWindow => ({ scope: "server", mode: "frozen", active: true, message: "", startsAt: "2026-11-16T09:00:00Z", endsAt: "", org: "", project: "", ...over });

describe("MaintenanceBanner", () => {
  it("renders nothing without a window", () => {
    const { container } = renderWithProviders(<MaintenanceBanner state={{}} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows an active frozen window with its start", () => {
    renderWithProviders(<MaintenanceBanner state={maintenanceState([w()])} />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("The server is frozen for maintenance");
    expect(alert).toHaveTextContent("Sign-in and changes are paused.");
    expect(alert).toHaveTextContent(`Since ${formatLocal("2026-11-16T09:00:00Z")}`);
    expect(alert).not.toHaveTextContent("Estimated end");
    expect(alert.className).toContain("bg-danger-soft");
  });

  it("prefers the active window and shows both times", () => {
    const active = w({ mode: "read_only", scope: "org", org: "acme", endsAt: "2026-11-16T10:00:00Z" });
    renderWithProviders(<MaintenanceBanner state={maintenanceState([w({ active: false, mode: "full" }), active])} />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Organization acme is read-only for maintenance");
    expect(alert).toHaveTextContent(`Since ${formatLocal(active.startsAt)} · Estimated end ${formatLocal(active.endsAt)}`);
  });

  it("lets a scheduled window be dismissed", async () => {
    renderWithProviders(<MaintenanceBanner state={maintenanceState([w({ active: false, mode: "full", message: "Upgrade", endsAt: "2026-11-16T10:00:00Z" })])} />);
    const status = screen.getByRole("status");
    expect(status).toHaveTextContent(`The server will be closed for maintenance from ${formatLocal("2026-11-16T09:00:00Z")}`);
    expect(status).toHaveTextContent("Upgrade");
    expect(status).not.toHaveTextContent("Since");
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByRole("status")).toBeNull();
  });
});
