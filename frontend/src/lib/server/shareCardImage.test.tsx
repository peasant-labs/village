// @vitest-environment node
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import YAML from "yaml";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ShareCardData } from "@peasant-labs/fairtrade/share";

// How drawing behaves: the real fairtrade card, or a card that throws.
const draw = vi.hoisted(() => ({ mode: "ok" as "ok" | "card-fails" | "all-fail" }));
// Whether the card fonts can be read from disk.
const disk = vi.hoisted(() => ({ fontsFail: false }));

vi.mock("node:fs/promises", async (importOriginal) => {
  const actual = await importOriginal<typeof import("node:fs/promises")>();
  const readFile = ((file: Parameters<typeof actual.readFile>[0], ...rest: unknown[]) => {
    if (disk.fontsFail && String(file).includes("share-fonts")) return Promise.reject(new Error("font read failed"));
    return (actual.readFile as (...args: unknown[]) => unknown)(file, ...rest);
  }) as typeof actual.readFile;
  return { ...actual, default: { ...actual, readFile }, readFile };
});

vi.mock("@peasant-labs/fairtrade/share", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@peasant-labs/fairtrade/share")>();
  return {
    ...actual,
    ShareCard: (props: Parameters<typeof actual.ShareCard>[0]) => {
      if (draw.mode === "all-fail" || (draw.mode === "card-fails" && props.card)) throw new Error("draw failed");
      return actual.ShareCard(props);
    },
  };
});

// The data cache exists only inside a running Next server.
vi.mock("next/cache", () => ({
  unstable_cache: <T,>(fn: () => Promise<T>) => fn,
}));

vi.mock("./loadShareCard", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./loadShareCard")>();
  return { ...actual, loadShareCard: vi.fn(actual.loadShareCard) };
});

type Lookup = "card" | "null" | "reject" | "hang";
interface ImageCase {
  name: string;
  lookup: Lookup;
  render: "ok" | "card-fails" | "all-fail";
  warm?: boolean;
  fonts?: "fail-once";
  expect: "card" | "generic" | "plain";
}
interface MetadataCase {
  name: string;
  id: string;
}

const fixture = YAML.parse(readFileSync(resolve(process.cwd(), "testdata/share-card/image-cases.yaml"), "utf8")) as {
  imageCases: ImageCase[];
  metadataCases: MetadataCase[];
};

const REQUIRED_IMAGE = [
  "lookup-rejects",
  "lookup-times-out",
  "card-render-rejects",
  "generic-render-fails-too",
  "kept-generic-outlives-a-broken-renderer",
  "fonts-are-read-again-after-a-failure",
  "public-card-renders",
];
const REQUIRED_METADATA = ["metadata-lookup-throws"];

const CARD: ShareCardData = {
  title: "rewrite billing retries",
  harness: "claude-code",
  brand: "claude",
  chip: "Claude Code (Opus 5.5)",
  firstPrompt: "rewrite billing retries with exponential backoff",
  firstPromptIsTitle: false,
  figures: { prompts: 6, tokens: 84000 },
};

const HEIGHTS = [600, 630] as const;

/** A fresh copy of the route module and its mocked loader, so kept images and fonts start empty. */
async function freshRoute() {
  vi.resetModules();
  const route = await import("./shareCardImage");
  const loader = await import("./loadShareCard");
  return { shareCardImage: route.shareCardImage, loadShareCard: vi.mocked(loader.loadShareCard) };
}

function setLookup(loadShareCard: Awaited<ReturnType<typeof freshRoute>>["loadShareCard"], lookup: Lookup) {
  if (lookup === "card") loadShareCard.mockResolvedValue(CARD);
  if (lookup === "null") loadShareCard.mockResolvedValue(null);
  if (lookup === "reject") loadShareCard.mockRejectedValue(new Error("lookup failed"));
  // `hang` keeps the real loader; the API below never answers.
}

async function serve(height: 600 | 630, route: Awaited<ReturnType<typeof freshRoute>>, fakeClock: boolean) {
  const pending = route.shareCardImage(Promise.resolve({ id: "7f1d2c3b" }), height);
  if (fakeClock) {
    await vi.advanceTimersByTimeAsync(3000);
    vi.useRealTimers();
  }
  const response = await pending;
  return { response, bytes: Buffer.from(await response.arrayBuffer()) };
}

function pngSize(bytes: Buffer): { width: number; height: number } {
  expect(bytes.subarray(0, 8)).toEqual(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]));
  expect(bytes.subarray(12, 16).toString("ascii")).toBe("IHDR");
  return { width: bytes.readUInt32BE(16), height: bytes.readUInt32BE(20) };
}

describe("share image route: fails closed", () => {
  const generic = new Map<number, Buffer>();

  beforeEach(async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.stubEnv("VILLAGE_INTERNAL_API_URL", "http://backend:8080/api/v1");
    vi.stubEnv("VILLAGE_PUBLIC_ORIGIN", "https://village.example");
    vi.stubGlobal("fetch", vi.fn((_url: string, init?: RequestInit) => new Promise((_, reject) => {
      init?.signal?.addEventListener("abort", () => reject(init.signal?.reason));
    })));
    draw.mode = "ok";
    disk.fontsFail = false;
    if (generic.size === 0) {
      for (const height of HEIGHTS) {
        const route = await freshRoute();
        route.loadShareCard.mockResolvedValue(null);
        generic.set(height, (await serve(height, route, false)).bytes);
      }
    }
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("has every required case", () => {
    expect(fixture.imageCases.map((c) => c.name)).toEqual(expect.arrayContaining(REQUIRED_IMAGE));
    expect(fixture.metadataCases.map((c) => c.name)).toEqual(expect.arrayContaining(REQUIRED_METADATA));
  });

  for (const c of fixture.imageCases) {
    for (const height of HEIGHTS) {
      it(`${c.name} (${height})`, async () => {
        const route = await freshRoute();
        if (c.warm) {
          route.loadShareCard.mockResolvedValue(null);
          await serve(height, route, false);
        }
        if (c.fonts === "fail-once") {
          route.loadShareCard.mockResolvedValue(null);
          disk.fontsFail = true;
          const first = await serve(height, route, false);
          expect(first.bytes.equals(generic.get(height)!), "drawn without the card fonts").toBe(false);
          disk.fontsFail = false;
        }
        setLookup(route.loadShareCard, c.lookup);
        draw.mode = c.render;
        if (c.lookup === "hang") vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });

        const { response, bytes } = await serve(height, route, c.lookup === "hang");

        expect(response.status).toBe(200);
        expect(response.headers.get("content-type")).toBe("image/png");
        expect(response.headers.get("cache-control")).toBe("no-store");
        expect(response.headers.get("x-robots-tag")).toBe("noindex");
        expect(pngSize(bytes)).toEqual({ width: 1200, height });
        if (c.expect === "generic") expect(bytes.equals(generic.get(height)!)).toBe(true);
        else expect(bytes.equals(generic.get(height)!)).toBe(false);
      }, 20_000);
    }
  }
});

describe("transcript page metadata: fails closed", () => {
  afterEach(() => vi.restoreAllMocks());

  for (const c of fixture.metadataCases) {
    it(c.name, async () => {
      vi.resetModules();
      const loader = await import("./loadShareCard");
      vi.mocked(loader.loadShareCard).mockRejectedValue(new Error("lookup failed"));
      const { generateMetadata } = await import("@/app/transcripts/[id]/page");
      const { shareCardMetadata } = await import("@/app/transcripts/[id]/shareCardMetadata");
      const metadata = await generateMetadata({ params: Promise.resolve({ id: c.id }) });
      expect(metadata).toEqual(shareCardMetadata(null, c.id));
    });
  }
});
