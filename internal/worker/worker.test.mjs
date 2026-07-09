// Node.js test suite for the Cloudflare Worker script (worker.js).
//
// Why this file exists: worker_test.go and worker_prefix_test.go only
// re-implement the Worker's JS logic *in Go* and assert against that
// re-implementation — they can never catch a real JS runtime bug (wrong
// scope, undefined variable, etc.) because they never execute the actual
// worker.js. That gap is exactly how a `ReferenceError` (block-scoped
// `const m` used outside its block) shipped to production and surfaced
// to users as Cloudflare error 1101 ("Worker threw exception") on every
// non-streaming response. This suite imports and executes the real
// worker.js module under Node so that class of bug fails fast, locally,
// with `go build`/CI wired to nothing extra.
//
// Requires Node.js 18+ (built-in fetch/Request/Response/Headers/URL/
// TransformStream globals, no npm dependencies). Run with:
//
//   node --test internal/worker/worker.test.mjs
//
// This is intentionally NOT wired into `go test` — it's a separate,
// optional check for whoever touches worker.js. Consider adding it to
// CI as a parallel step (`node --test internal/worker/*.test.mjs`).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import worker from './worker.js';

const UPSTREAM = 'https://upstream.example.test/api/coding/paas/v4';
const API_KEY = 'upstream-secret-key';
const CURSOR_KEY = 'cursor-gateway-key';
const TEST_KEY = 'testkey_41324124#$!F';

const FWD = JSON.stringify([['z.ai/glm-5.2', 'glm-5.2']]);
const REV = JSON.stringify([['glm-5.2', 'z.ai/glm-5.2']]);

function baseEnv(overrides = {}) {
  return {
    UPSTREAM,
    API_KEY,
    CURSOR_KEY,
    TEST_KEY,
    MODEL_MAPPINGS: FWD,
    MODEL_REVERSE: REV,
    API_STYLE: 'both',
    ...overrides,
  };
}

// Installs a fake global fetch that answers upstream requests without any
// network access. Returns a restore function.
function stubUpstreamFetch(handler) {
  const original = globalThis.fetch;
  globalThis.fetch = async (req) => handler(req);
  return () => { globalThis.fetch = original; };
}

test('non-streaming chat completion does not throw (regression: error 1101)', async () => {
  const restore = stubUpstreamFetch(async () => {
    return new Response(JSON.stringify({ id: 'abc', model: 'glm-5.2', choices: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  });
  try {
    const req = new Request('https://worker.test/v1/chat/completions', {
      method: 'POST',
      headers: { Authorization: `Bearer ${API_KEY}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'z.ai/glm-5.2', messages: [] }),
    });
    const res = await worker.fetch(req, baseEnv());
    assert.equal(res.status, 200, 'must not throw / must not 5xx on buffered JSON response');
    const body = await res.json();
    assert.equal(body.model, 'z.ai/glm-5.2', 'reverse model rewrite must still apply');
  } finally {
    restore();
  }
});

test('streaming (SSE) response passes through and rewrites model names', async () => {
  const restore = stubUpstreamFetch(async () => {
    const sse = 'data: {"model":"glm-5.2","id":"glm-5.2"}\n\ndata: [DONE]\n\n';
    return new Response(sse, {
      status: 200,
      headers: { 'Content-Type': 'text/event-stream' },
    });
  });
  try {
    const req = new Request('https://worker.test/v1/chat/completions', {
      method: 'POST',
      headers: { Authorization: `Bearer ${API_KEY}` },
      body: JSON.stringify({ model: 'z.ai/glm-5.2', messages: [], stream: true }),
    });
    const res = await worker.fetch(req, baseEnv());
    assert.equal(res.status, 200);
    const text = await res.text();
    assert.match(text, /"model":"z\.ai\/glm-5\.2"/);
    assert.match(text, /\[DONE\]/);
  } finally {
    restore();
  }
});

test('invalid key is rejected with 401 and never reaches upstream', async () => {
  const restore = stubUpstreamFetch(async () => {
    throw new Error('upstream must not be called for an unauthenticated request');
  });
  try {
    const req = new Request('https://worker.test/v1/chat/completions', {
      method: 'POST',
      headers: { Authorization: 'Bearer wrong-key' },
      body: '{}',
    });
    const res = await worker.fetch(req, baseEnv());
    assert.equal(res.status, 401);
  } finally {
    restore();
  }
});

test('/health is public and requires no key', async () => {
  const req = new Request('https://worker.test/health');
  const res = await worker.fetch(req, baseEnv());
  assert.equal(res.status, 200);
  assert.equal(await res.text(), 'OK');
});

test('/test accepts the built-in TEST_KEY', async () => {
  const req = new Request('https://worker.test/test', {
    headers: { Authorization: `Bearer ${TEST_KEY}` },
  });
  const res = await worker.fetch(req, baseEnv());
  assert.equal(res.status, 200);
  const body = await res.json();
  assert.equal(body.status, 'OK');
  assert.equal(body.matched, 'TEST_KEY');
});

test('/test accepts a composite key (CURSOR_KEY_<clientId>) and echoes the clientId', async () => {
  const req = new Request('https://worker.test/test', {
    headers: { Authorization: `Bearer ${CURSOR_KEY}_charlie` },
  });
  const res = await worker.fetch(req, baseEnv());
  assert.equal(res.status, 200);
  const body = await res.json();
  assert.equal(body.status, 'OK');
  assert.equal(body.clientId, 'charlie');
});

test('API_STYLE=openai rejects Anthropic /messages requests', async () => {
  const req = new Request('https://worker.test/v1/messages', {
    method: 'POST',
    headers: { Authorization: `Bearer ${API_KEY}` },
    body: '{}',
  });
  const res = await worker.fetch(req, baseEnv({ API_STYLE: 'openai' }));
  assert.equal(res.status, 403);
});

// Regression coverage for the constant-time matchKey rewrite: the
// prefix-matching semantics (`key + '_' + clientId`) must behave
// identically to the old `===`/`startsWith` implementation. This can't
// verify the *timing* property in a unit test, but it locks in
// behavioral correctness so the constant-time rewrite can't silently
// break auth (accepting truncated/extended keys, dropping clientId,
// etc.) while looking "constant-time".
test('matchKey semantics: exact key with no clientId suffix is accepted', async () => {
  const restore = stubUpstreamFetch(async () => new Response('{}', {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }));
  try {
    const req = new Request('https://worker.test/v1/chat/completions', {
      method: 'POST',
      headers: { Authorization: `Bearer ${CURSOR_KEY}` },
      body: JSON.stringify({ model: 'z.ai/glm-5.2', messages: [] }),
    });
    const res = await worker.fetch(req, baseEnv());
    assert.equal(res.status, 200);
  } finally {
    restore();
  }
});

test('matchKey semantics: prefix + "_" + clientId is accepted and clientId is extracted', async () => {
  const restore = stubUpstreamFetch(async () => new Response(JSON.stringify({ model: 'glm-5.2' }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }));
  try {
    const req = new Request('https://worker.test/v1/chat/completions', {
      method: 'POST',
      headers: { Authorization: `Bearer ${CURSOR_KEY}_my-client-42` },
      body: JSON.stringify({ model: 'z.ai/glm-5.2', messages: [] }),
    });
    const res = await worker.fetch(req, baseEnv());
    assert.equal(res.status, 200, 'valid key + clientId suffix must still authenticate');
  } finally {
    restore();
  }
});

test('matchKey semantics: truncated key (correct prefix, too short) is rejected', async () => {
  const restore = stubUpstreamFetch(async () => {
    throw new Error('upstream must not be called for an unauthenticated request');
  });
  try {
    const truncated = CURSOR_KEY.slice(0, CURSOR_KEY.length - 2);
    const req = new Request('https://worker.test/v1/chat/completions', {
      method: 'POST',
      headers: { Authorization: `Bearer ${truncated}` },
      body: '{}',
    });
    const res = await worker.fetch(req, baseEnv());
    assert.equal(res.status, 401, 'a key shorter than the configured key must never match');
  } finally {
    restore();
  }
});

test('matchKey semantics: correct prefix without "_" separator is rejected', async () => {
  const restore = stubUpstreamFetch(async () => {
    throw new Error('upstream must not be called for an unauthenticated request');
  });
  try {
    // Same bytes as the real key plus extra chars, but no "_" right
    // after the key — must NOT be treated as key + clientId.
    const req = new Request('https://worker.test/v1/chat/completions', {
      method: 'POST',
      headers: { Authorization: `Bearer ${CURSOR_KEY}XtraStuffNoUnderscore` },
      body: '{}',
    });
    const res = await worker.fetch(req, baseEnv());
    assert.equal(res.status, 401, 'key+suffix without "_" separator must be rejected, not fuzzy-matched');
  } finally {
    restore();
  }
});

test('matchKey semantics: single differing byte anywhere in the key is rejected', async () => {
  const restore = stubUpstreamFetch(async () => {
    throw new Error('upstream must not be called for an unauthenticated request');
  });
  try {
    // Flip the last character — a naive off-by-one in the constant-time
    // loop (e.g. wrong length used for slicing) could accept this.
    const almost = CURSOR_KEY.slice(0, -1) + (CURSOR_KEY.at(-1) === 'x' ? 'y' : 'x');
    const req = new Request('https://worker.test/v1/chat/completions', {
      method: 'POST',
      headers: { Authorization: `Bearer ${almost}` },
      body: '{}',
    });
    const res = await worker.fetch(req, baseEnv());
    assert.equal(res.status, 401);
  } finally {
    restore();
  }
});

// --- /stats endpoint ------------------------------------------------
//
// worker.js keeps its stats (`_stats`) as module-scope state (see the
// `Object.create(null)` at the top of worker.js) — it is NOT reset
// between requests, and since Node's ES module cache only evaluates
// './worker.js' once for this whole test file, that state persists
// across every test() in this file, in whatever order they run.
//
// To avoid cross-test pollution we do one of two things per test below:
//   - when we need an exact count (composite-key tests), we mint a
//     brand-new, never-before-used clientId suffix (e.g. "_statstest1")
//     so no other test in this file could have touched that bucket; or
//   - when we must inspect the shared "unknown" bucket (the bare-key,
//     no-clientId case — every test in this file that sends a bare
//     CURSOR_KEY/API_KEY lands in this same bucket), we snapshot
//     before/after and assert on the *delta*, never the absolute count.

async function getStats(key, opts = {}) {
  const qs = opts.all ? '?all=true' : '';
  const req = new Request(`https://worker.test/stats${qs}`, {
    headers: { Authorization: `Bearer ${key}` },
  });
  const res = await worker.fetch(req, baseEnv());
  return res;
}

test('/stats: bare CURSOR_KEY (no clientId) tracks request count in the shared "unknown" bucket', async () => {
  // Response body content is irrelevant here since we assert on the
  // *requests* delta only (see the token-counting bug note below).
  const restore = stubUpstreamFetch(async () => new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }));
  try {
    const before = await (await getStats(CURSOR_KEY)).json();

    const N = 2;
    for (let i = 0; i < N; i++) {
      const req = new Request('https://worker.test/v1/chat/completions', {
        method: 'POST',
        headers: { Authorization: `Bearer ${CURSOR_KEY}` },
        body: JSON.stringify({ model: 'irrelevant-model', messages: [] }),
      });
      const res = await worker.fetch(req, baseEnv());
      assert.equal(res.status, 200);
    }

    const afterRes = await getStats(CURSOR_KEY);
    assert.equal(afterRes.status, 200);
    const after = await afterRes.json();

    assert.equal(after.client, 'unknown');
    assert.equal(after.requests - before.requests, N, 'requests must increase by exactly N for the bare-key bucket');

    // BUG (found while writing this test, not fixed here per task
    // scope): worker.js's token accounting only fires when
    // `clientMatch.clientId` is truthy —
    //   if (clientMatch.clientId && _stats[clientMatch.clientId]) { ... tokens += ... }
    // but `clientMatch.clientId` is the *empty string* for a bare key
    // with no "_<clientId>" suffix, even though recordStats() buckets
    // that same request under the literal string 'unknown'
    // (`const id = clientId || 'unknown'`). So requests accumulate for
    // 'unknown' but tokens for 'unknown' can never increase — it is
    // permanently stuck at 0 for anyone hitting the Worker without a
    // client-id suffix on their key. This assertion locks in that
    // (buggy) observed behavior rather than the presumably-intended one.
    assert.equal(after.tokens - before.tokens, 0, 'documents current behavior: tokens never accrue for the no-clientId bucket (see bug note above)');
  } finally {
    restore();
  }
});

test('/stats: composite keys (CURSOR_KEY_alice / CURSOR_KEY_bob) are tracked per-client and tokens accrue', async () => {
  // Unique, never-reused clientId suffixes so this test's assertions
  // can rely on exact counts instead of deltas.
  const alice = 'statstest_alice_1';
  const bob = 'statstest_bob_1';

  const responseBody = JSON.stringify({ ok: true, filler: 'x'.repeat(36) });
  const expectedTokens = Math.ceil(responseBody.length / 4);
  const restore = stubUpstreamFetch(async () => new Response(responseBody, {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }));
  try {
    async function post(clientKey) {
      const req = new Request('https://worker.test/v1/chat/completions', {
        method: 'POST',
        headers: { Authorization: `Bearer ${clientKey}` },
        body: JSON.stringify({ model: 'irrelevant-model', messages: [] }),
      });
      const res = await worker.fetch(req, baseEnv());
      assert.equal(res.status, 200);
    }

    // 3 requests as alice, 1 as bob.
    await post(`${CURSOR_KEY}_${alice}`);
    await post(`${CURSOR_KEY}_${alice}`);
    await post(`${CURSOR_KEY}_${alice}`);
    await post(`${CURSOR_KEY}_${bob}`);

    const aliceRes = await getStats(`${CURSOR_KEY}_${alice}`);
    assert.equal(aliceRes.status, 200);
    const aliceStats = await aliceRes.json();
    assert.equal(aliceStats.client, alice);
    assert.equal(aliceStats.requests, 3);
    assert.equal(aliceStats.tokens, 3 * expectedTokens, 'tokens accrue per request when a clientId suffix is present');

    const allRes = await getStats(`${CURSOR_KEY}_${alice}`, { all: true });
    assert.equal(allRes.status, 200);
    const all = await allRes.json();
    const aliceEntry = all.find((e) => e.client === alice);
    const bobEntry = all.find((e) => e.client === bob);
    assert.ok(aliceEntry, 'alice must appear in /stats?all=true');
    assert.ok(bobEntry, 'bob must appear in /stats?all=true');
    assert.equal(aliceEntry.requests, 3);
    assert.equal(bobEntry.requests, 1);
    assert.equal(bobEntry.tokens, expectedTokens);
  } finally {
    restore();
  }
});

test('/stats: wrong key is rejected with 401', async () => {
  const res = await getStats('totally-wrong-key');
  assert.equal(res.status, 401);
  const body = await res.json();
  assert.equal(body.error, 'unauthorized');
});

test('client-supplied clientId cannot pollute Object.prototype', async () => {
  const restore = stubUpstreamFetch(async () => {
    return new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  });
  try {
    const req = new Request('https://worker.test/v1/chat/completions', {
      method: 'POST',
      // clientId is everything after "<key>_" — try to smuggle a
      // dunder-proto key via the gateway key's client-id suffix.
      headers: { Authorization: `Bearer ${CURSOR_KEY}_toString` },
      body: JSON.stringify({ model: 'z.ai/glm-5.2', messages: [] }),
    });
    const res = await worker.fetch(req, baseEnv());
    assert.equal(res.status, 200);
    assert.equal(typeof ({}).toString, 'function', 'Object.prototype.toString must be untouched');
    assert.equal(Object.getPrototypeOf({}), Object.prototype, 'global Object.prototype must be untouched');
  } finally {
    restore();
  }
});
