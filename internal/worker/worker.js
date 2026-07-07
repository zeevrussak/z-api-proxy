// Z-API Proxy Cloudflare Worker
// https://github.com/zeevrussak/z-api-proxy
//
// ATTRIBUTION: Powered by z-api-proxy by Zeev Russak
// License: Attribution Required — see LICENSE
//
// Configuration is via Cloudflare environment variables (set on deploy):
//   UPSTREAM         (var)    — z.ai API base URL
//   MODEL_MAPPINGS   (var)    — JSON: [["z.ai/glm-5.2","glm-5.2"], ...]
//   MODEL_REVERSE    (var)    — JSON: [["glm-5.2","z.ai/glm-5.2"], ...]
//   API_KEY          (secret) — z.ai upstream API key
//   CURSOR_KEY       (secret) — key Cursor sends (validated, swapped for API_KEY)
//   TEST_KEY         (secret) — built-in test key for deployment verification

// Stats are stored in a global Map (resets on Worker restart, but survives
// across requests within the same isolate lifetime).
const _stats = {};

function matchKey(sentKey, expectedKey) {
  if (!sentKey || !expectedKey) return { ok: false, clientId: '' };
  if (sentKey === expectedKey) return { ok: true, clientId: '' };
  if (sentKey.startsWith(expectedKey + '_')) {
    return { ok: true, clientId: sentKey.substring(expectedKey.length + 1) };
  }
  return { ok: false, clientId: '' };
}

function matchAnyKey(sentKey, keys) {
  for (const k of keys) {
    if (!k) continue;
    const m = matchKey(sentKey, k);
    if (m.ok) return m;
  }
  return { ok: false, clientId: '' };
}

function recordStats(clientId) {
  const id = clientId || 'unknown';
  if (!_stats[id]) _stats[id] = { requests: 0, tokens: 0, lastSeen: '' };
  _stats[id].requests++;
  _stats[id].lastSeen = new Date().toISOString();
}

const HOP_HEADERS = new Set([
  'connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization',
  'te', 'trailers', 'transfer-encoding', 'upgrade', 'accept-encoding'
]);

export default {
  async fetch(request, env) {
    const UPSTREAM = env.UPSTREAM || 'https://api.z.ai/api/coding/paas/v4';
    const API_KEY = env.API_KEY || '';
    const CURSOR_KEY = env.CURSOR_KEY || '';
    const FORWARD_MAP = new Map(JSON.parse(env.MODEL_MAPPINGS || '[]'));
    const REVERSE_MAP = new Map(JSON.parse(env.MODEL_REVERSE || '[]'));
    const url = new URL(request.url);

    const acceptedKeys = [API_KEY];
    if (CURSOR_KEY) acceptedKeys.push(CURSOR_KEY);

    const authHeader = request.headers.get('Authorization') || '';
    const xApiKey = request.headers.get('x-api-key') || '';
    const sentKey = authHeader.replace('Bearer ', '') || xApiKey;
    const sentPrefix = sentKey.substring(0, 12);

    console.log('[z-api-proxy] ' + request.method + ' ' + url.pathname + ' key=' + (sentPrefix ? sentPrefix + '...' : 'NONE'));

    // /health is public — just a liveness check.
    if (url.pathname === '/health') {
      return new Response('OK', { status: 200 });
    }

    const TEST_KEY = env.TEST_KEY || '';
    const allKeys = [...acceptedKeys];
    if (TEST_KEY) allKeys.push(TEST_KEY);

    // /test endpoint: prefix-matches any accepted key.
    if (url.pathname === '/test') {
      const m = matchAnyKey(sentKey, allKeys);
      if (m.ok) {
        return new Response(JSON.stringify({
          status: 'OK',
          matched: sentKey === TEST_KEY ? 'TEST_KEY' : 'ACCEPTED',
          clientId: m.clientId || ''
        }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      return new Response(JSON.stringify({
        status: 'FAIL',
        message: 'No matching key',
        received: sentPrefix ? sentPrefix + '...' : 'NONE'
      }), { status: 401, headers: { 'Content-Type': 'application/json' } });
    }

    // /stats endpoint: returns per-client usage stats as JSON.
    // Requires any valid key. Client ID extracted from key prefix.
    if (url.pathname === '/stats') {
      const m = matchAnyKey(sentKey, allKeys);
      if (!m.ok) {
        return new Response(JSON.stringify({ error: 'unauthorized' }), {
          status: 401, headers: { 'Content-Type': 'application/json' }
        });
      }
      const showAll = url.searchParams.get('all') === 'true';
      if (showAll) {
        const arr = Object.entries(_stats).map(([client, s]) => ({
          client, requests: s.requests, tokens: s.tokens, lastSeen: s.lastSeen
        }));
        return new Response(JSON.stringify(arr), {
          status: 200, headers: { 'Content-Type': 'application/json' }
        });
      }
      const id = m.clientId || 'unknown';
      const s = _stats[id] || { requests: 0, tokens: 0, lastSeen: '' };
      return new Response(JSON.stringify({
        client: id, requests: s.requests, tokens: s.tokens, lastSeen: s.lastSeen
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }

    // /v1/models is public — returns capabilities without auth.
    if (url.pathname === '/v1/models' || url.pathname === '/models') {
      const MODEL_SPECS = {
        'z.ai/gielem52/1M':    { ctx: 1048576, maxOut: 131072 },
        'z.ai/glm-5.2/200k':  { ctx: 200000,  maxOut: 131072 },
        'z.ai/glm-5.1/1M':    { ctx: 1048576, maxOut: 131072 },
        'z.ai/glm-5':         { ctx: 131072,  maxOut: 131072 },
        'z.ai/glm-5-turbo':   { ctx: 131072,  maxOut: 131072 },
        'z.ai/glm-5v-turbo':  { ctx: 131072,  maxOut: 131072 },
        'z.ai/glm-4.7':       { ctx: 131072,  maxOut: 131072 },
        'z.ai/glm-4.7-flash': { ctx: 131072,  maxOut: 131072 },
        'z.ai/glm-4.7-flashx':{ ctx: 131072,  maxOut: 131072 },
        'z.ai/glm-4.6':       { ctx: 200000,  maxOut: 131072 },
        'z.ai/glm-4.6v':      { ctx: 131072,  maxOut: 32768 },
        'z.ai/glm-4.5':       { ctx: 131072,  maxOut: 98304 },
        'z.ai/glm-4.5-air':   { ctx: 131072,  maxOut: 98304 },
        'z.ai/glm-4.5-flash': { ctx: 131072,  maxOut: 98304 },
        'z.ai/glm-4.5v':      { ctx: 131072,  maxOut: 16384 },
      };
      const models = [];
      for (const [cursorName, upstreamName] of FORWARD_MAP) {
        const spec = MODEL_SPECS[cursorName] || { ctx: 131072, maxOut: 65536 };
        models.push({
          id: cursorName, object: 'model', created: 1700000000, owned_by: 'z.ai',
          context_length: spec.ctx, max_context_length: spec.ctx,
          context_window: spec.ctx, max_input_tokens: spec.ctx,
          max_tokens: spec.maxOut, max_output_tokens: spec.maxOut
        });
      }
      return new Response(JSON.stringify({ object: 'list', data: models }), {
        status: 200, headers: { 'Content-Type': 'application/json' }
      });
    }

    // Main request flow — prefix-match keys, extract client ID.
    let reqBody = null;
    const init = { method: request.method, headers: {} };
    for (const [key, value] of request.headers.entries()) {
      const lk = key.toLowerCase();
      if (HOP_HEADERS.has(lk)) continue;
      if (lk === 'authorization' || lk === 'x-api-key') continue;
      init.headers[key] = value;
    }

    if (request.method === 'POST' || request.method === 'PUT' || request.method === 'PATCH') {
      reqBody = await request.text();
      for (const [from, to] of FORWARD_MAP) {
        reqBody = reqBody.replaceAll('"model":"' + from + '"', '"model":"' + to + '"');
        reqBody = reqBody.replaceAll('"model": "' + from + '"', '"model": "' + to + '"');
      }
      init.body = reqBody;
    }

    if (API_KEY) {
      const m = matchAnyKey(sentKey, acceptedKeys);
      if (!m.ok) {
        return new Response(JSON.stringify({
          error: { message: 'Invalid API key.', type: 'invalid_request_error', code: 'invalid_api_key' }
        }), { status: 401, headers: { 'Content-Type': 'application/json' } });
      }
      // Record stats for this client.
      recordStats(m.clientId);
      // Forward with real upstream key.
      init.headers['Authorization'] = 'Bearer ' + API_KEY;
      init.headers['x-api-key'] = API_KEY;
    } else {
      return new Response(JSON.stringify({
        error: { message: 'Worker has no upstream API key configured.', type: 'invalid_request_error' }
      }), { status: 401, headers: { 'Content-Type': 'application/json' } });
    }

    let upstreamPath = url.pathname.replace(/^\/v1/, '');
    if (!upstreamPath.startsWith('/')) upstreamPath = '/' + upstreamPath;
    const upstreamUrl = UPSTREAM + upstreamPath + url.search;

    const upstreamReq = new Request(upstreamUrl, init);
    let upstreamResp;
    try {
      upstreamResp = await fetch(upstreamReq);
    } catch (e) {
      return new Response(JSON.stringify({
        error: { message: 'upstream unreachable: ' + e.message, type: 'server_error' }
      }), { status: 502, headers: { 'Content-Type': 'application/json' } });
    }

    const respHeaders = new Headers();
    for (const [key, value] of upstreamResp.headers.entries()) {
      if (!HOP_HEADERS.has(key.toLowerCase())) {
        respHeaders.set(key, value);
      }
    }

    const contentType = upstreamResp.headers.get('Content-Type') || '';

    if (contentType.includes('text/event-stream')) {
      const { readable, writable } = new TransformStream();
      (async () => {
        const reader = upstreamResp.body.getReader();
        const decoder = new TextDecoder();
        const writer = writable.getWriter();
        const encoder = new TextEncoder();
        let buffer = '';
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop();
          for (const line of lines) {
            let out = line;
            if (line.trim().startsWith('data:') && !line.includes('[DONE]')) {
              for (const [from, to] of REVERSE_MAP) {
                out = out.replaceAll('"model":"' + from + '"', '"model":"' + to + '"');
                out = out.replaceAll('"model": "' + from + '"', '"model": "' + to + '"');
                out = out.replaceAll('"id":"' + from + '"', '"id":"' + to + '"');
                out = out.replaceAll('"id": "' + from + '"', '"id": "' + to + '"');
              }
            }
            writer.write(encoder.encode(out + '\n'));
          }
        }
        if (buffer) writer.write(encoder.encode(buffer));
        writer.close();
      })();
      return new Response(readable, { status: upstreamResp.status, headers: respHeaders });
    }

    let respBody = await upstreamResp.text();
    // Estimate tokens from response size (rough: 4 chars = 1 token).
    if (m.clientId && _stats[m.clientId]) {
      _stats[m.clientId].tokens += Math.ceil(respBody.length / 4);
    }
    for (const [from, to] of REVERSE_MAP) {
      respBody = respBody.replaceAll('"model":"' + from + '"', '"model":"' + to + '"');
      respBody = respBody.replaceAll('"model": "' + from + '"', '"model": "' + to + '"');
      respBody = respBody.replaceAll('"id":"' + from + '"', '"id":"' + to + '"');
      respBody = respBody.replaceAll('"id": "' + from + '"', '"id": "' + to + '"');
    }
    respHeaders.set('Content-Length', new TextEncoder().encode(respBody).length.toString());
    return new Response(respBody, { status: upstreamResp.status, headers: respHeaders });
  }
};
