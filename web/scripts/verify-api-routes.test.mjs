import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { verifyFrontendRoutes } from './verify-api-routes.mjs'

function fixture(manifest, source) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'hserver-frontend-routes-'))
  fs.mkdirSync(path.join(root, 'internal/api'), { recursive: true })
  fs.mkdirSync(path.join(root, 'web/src'), { recursive: true })
  fs.writeFileSync(path.join(root, 'internal/api/routes_manifest.go'), manifest)
  fs.writeFileSync(path.join(root, 'web/src/example.ts'), source)
  return root
}

function addRequestContract(root, route, method, schemaName, schema) {
  fs.mkdirSync(path.join(root, 'docs'), { recursive: true })
  fs.writeFileSync(path.join(root, 'docs/openapi.json'), JSON.stringify({
    paths: {
      [route]: {
        [method.toLowerCase()]: {
          requestBody: {
            content: { 'application/json': { schema: { $ref: `#/components/schemas/${schemaName}` } } },
          },
        },
      },
    },
    components: { schemas: { [schemaName]: schema } },
  }))
}

test('accepts a frontend template that matches the method and route shape', (t) => {
  const root = fixture(
    '{"POST", "/api/pm2/processes/{id}/{action}", RouteManager},',
    'api.post(`/pm2/processes/${id}/delete`)',
  )
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({ root, write: () => {}, writeError: () => {} })

  assert.equal(result.ok, true)
  assert.equal(result.verified, 1)
  assert.equal(result.unverified, 0)
})

test('rejects a frontend method that is not registered for the route', (t) => {
  const root = fixture(
    '{"POST", "/api/pm2/processes/{id}/{action}", RouteManager},',
    'api.delete(`/pm2/processes/${id}/delete`)',
  )
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({ root, write: () => {}, writeError: () => {} })

  assert.equal(result.ok, false)
  assert.equal(result.failures[0].candidate, '/api/pm2/processes/{dynamic}/delete')
})

test('reports helper-built routes without claiming they were verified', (t) => {
  const root = fixture(
    '{"POST", "/api/nodes/{id}/tasks", RouteManager},',
    'api.post(managedNodePath(server, "/tasks"))',
  )
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({ root, write: () => {}, writeError: () => {} })

  assert.equal(result.ok, true)
  assert.equal(result.verified, 0)
  assert.equal(result.unverified, 1)
})

test('can print the source location and expression for unresolved calls', (t) => {
  const root = fixture(
    '{"POST", "/api/nodes/{id}/tasks", RouteManager},',
    'api.post(managedNodePath(server, "/tasks"))',
  )
  const output = []
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({
    root,
    reportUnverified: true,
    write: (line) => output.push(line),
    writeError: () => {},
  })

  assert.equal(result.unverifiedCalls.length, 1)
  assert.match(output.join('\n'), /example\.ts:1 POST managedNodePath\(server, "\/tasks"\)/)
})

test('checks every statically known branch of a local path constant', (t) => {
  const root = fixture(
    [
      '{"GET", "/api/domains", RouteProtected},',
      '{"GET", "/api/nodes/{id}/domains", RouteAdmin},',
    ].join('\n'),
    'const base = remote ? `/nodes/${nodeID}` : ""; api.get(`${base}/domains`)',
  )
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({ root, write: () => {}, writeError: () => {} })

  assert.equal(result.ok, true)
  assert.equal(result.verified, 1)
  assert.equal(result.unverified, 0)
})

test('resolves an annotated route helper at its call site', (t) => {
  const root = fixture(
    '{"GET", "/api/nodes/{id}/memory", RouteAdmin},',
    [
      '/** @apiRoute /nodes/{server}{suffix} */',
      'function managedNodePath(server: string, suffix: string) { return `/nodes/${server}${suffix}` }',
      'api.get(managedNodePath(nodeID, "/memory"))',
    ].join('\n'),
  )
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({ root, write: () => {}, writeError: () => {} })

  assert.equal(result.ok, true)
  assert.equal(result.verified, 1)
  assert.equal(result.unverified, 0)
})

test('expands finite string action types against static backend routes', (t) => {
  const root = fixture(
    [
      '{"POST", "/api/system/actions/start", RouteAdmin},',
      '{"POST", "/api/system/actions/stop", RouteAdmin},',
    ].join('\n'),
    'type Action = "start" | "stop"; function run(action: Action) { api.post(`/system/actions/${action}`) }',
  )
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({ root, write: () => {}, writeError: () => {} })

  assert.equal(result.ok, true)
  assert.equal(result.verified, 1)
})

test('accepts a frontend body that satisfies a promoted request schema', (t) => {
  const root = fixture(
    '{"POST", "/api/nodes", RouteAdmin},',
    'const nodeID: string = "edge-1"; api.post("/nodes", { id: nodeID, name: "Edge" })',
  )
  addRequestContract(root, '/api/nodes', 'POST', 'NodeRequest', {
    type: 'object',
    additionalProperties: false,
    required: ['id', 'name'],
    properties: { id: { type: 'string' }, name: { type: 'string' } },
  })
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({ root, write: () => {}, writeError: () => {} })

  assert.equal(result.ok, true)
  assert.equal(result.payloadVerified, 1)
  assert.equal(result.payloadTotal, 1)
})

test('accepts confirmed managed-node task bodies from both callers', (t) => {
  const root = fixture(
    '{"POST", "/api/nodes/{id}/tasks", RouteManager},',
    [
      'const nodeID: string = "edge-1"',
      'const nodeBase = `/nodes/${nodeID}`',
      'api.post(`/nodes/${nodeID}/tasks`, { kind: "service.action", payload: { service: "nginx.service", action: "restart" }, confirmed: true })',
      'api.post(nodeBase + "/tasks", { kind: "service.action", payload: { service: "nginx.service", action: "restart" }, confirmed: true })',
    ].join('\n'),
  )
  addRequestContract(root, '/api/nodes/{id}/tasks', 'POST', 'AgentTaskRequest', {
    type: 'object',
    additionalProperties: false,
    required: ['kind', 'confirmed'],
    properties: {
      kind: { type: 'string', enum: ['service.action'] },
      payload: { type: 'object', maxProperties: 6, additionalProperties: { type: 'string' } },
      confirmed: { type: 'boolean', const: true },
    },
  })
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({ root, write: () => {}, writeError: () => {} })

  assert.equal(result.ok, true)
  assert.equal(result.payloadTotal, 2)
  assert.equal(result.payloadVerified, 2)
  assert.deepEqual(result.payloadFailures, [])
})

test('rejects missing, unknown, and unconstrained request fields', (t) => {
  const root = fixture(
    '{"POST", "/api/nodes", RouteAdmin},',
    'const kind: string = "edge"; api.post("/nodes", { id: "edge-1", label: "Edge", kind })',
  )
  addRequestContract(root, '/api/nodes', 'POST', 'NodeRequest', {
    type: 'object',
    additionalProperties: false,
    required: ['id', 'name'],
    properties: {
      id: { type: 'string' },
      name: { type: 'string' },
      kind: { type: 'string', enum: ['edge', 'core'] },
    },
  })
  t.after(() => fs.rmSync(root, { recursive: true, force: true }))

  const result = verifyFrontendRoutes({ root, write: () => {}, writeError: () => {} })

  assert.equal(result.ok, false)
  assert.deepEqual(result.payloadFailures[0].errors, [
    'body.name is missing',
    'body.label is not allowed',
    'body.kind is not constrained to edge|core',
  ])
})
