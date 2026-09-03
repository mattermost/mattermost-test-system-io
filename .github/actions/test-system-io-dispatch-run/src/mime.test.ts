import { test } from "node:test";
import * as assert from "node:assert/strict";
import { imageContentType, screenshotContentType } from "./mime.ts";

test("screenshotContentType: png and jpeg by extension", () => {
  assert.equal(screenshotContentType("join_call-1.png"), "image/png");
  assert.equal(screenshotContentType("/tmp/artifacts/fail.JPG"), "image/jpeg");
  assert.equal(screenshotContentType("shot.jpeg"), "image/jpeg");
});

test("screenshotContentType: unknown extension defaults to image/png", () => {
  assert.equal(screenshotContentType("notes.txt"), "image/png");
});

test("imageContentType: returns null for non-image paths", () => {
  assert.equal(imageContentType("notes.txt"), null);
  assert.equal(imageContentType("report.json"), null);
});
