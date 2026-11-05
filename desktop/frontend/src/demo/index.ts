import { createService, scenarioFrom } from "./service";

export const Service = createService(scenarioFrom(typeof location === "undefined" ? "" : location.search));
