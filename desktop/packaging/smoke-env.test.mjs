import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";
import { packagedSmokeEnv } from "./smoke-env.mjs";

test("packaged startup uses an isolated home without inherited development or service overrides", () => {
  const parent = {
    PATH: "system-path", DISPLAY: ":99", HOME: "system-home",
    PONDERA_DEV: "1", Pondera_Dev: "1", PONDERA_CHANNEL: "dev", PONDERA_COMMIT: "old",
    PONDERA_DESKTOP_SERVICE: "old-service", PONDERA_ELECTRON_DEV_URL: "http://localhost:5173",
    PONDERA_HOME: "real-user-data", PONDERA_STATE_HOME: "real-state", PONDERA_CACHE_HOME: "real-cache",
    NODE_OPTIONS: "--require development-hook", ELECTRON_RUN_AS_NODE: "1",
  };
  assert.deepEqual(packagedSmokeEnv(parent, "fixture"), {
    PATH: "system-path", DISPLAY: ":99", HOME: "system-home",
    PONDERA_HOME: "fixture", PONDERA_STATE_HOME: "fixture", PONDERA_CACHE_HOME: join("fixture", "cache"),
  });
  assert.equal(parent.PONDERA_DEV, "1", "the caller environment is not mutated");
});
