import { describe, expect, it } from "vitest";
import { SDK_VERSION } from "./index.js";

describe("@cronix/sdk", () => {
  it("exports a version string", () => {
    expect(typeof SDK_VERSION).toBe("string");
  });
});
