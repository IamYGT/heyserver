import fs from 'node:fs'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const apiMethods = new Set(['get', 'post', 'put', 'delete'])
const manifestRoutePattern = /\{"([A-Z]+)", "([^"]+)", Route[A-Za-z]+\},/g

function collectSourceFiles(directory) {
  const files = []
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const fullPath = path.join(directory, entry.name)
    if (entry.isDirectory()) {
      files.push(...collectSourceFiles(fullPath))
      continue
    }
    if (!/\.(ts|tsx)$/.test(entry.name) || /\.(test|spec)\.(ts|tsx)$/.test(entry.name)) continue
    files.push(fullPath)
  }
  return files
}

function parseManifest(source) {
  const routes = []
  for (const match of source.matchAll(manifestRoutePattern)) {
    routes.push({ method: match[1], path: match[2] })
  }
  return routes
}

function collectConstInitializers(sourceFile) {
  const initializers = new Map()
  function visit(node) {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer) {
      initializers.set(node.name.text, node.initializer)
    }
    ts.forEachChild(node, visit)
  }
  visit(sourceFile)
  return initializers
}

function literalAlternatives(node, checker) {
  if (!checker) return null
  const type = checker.getTypeAtLocation(node)
  const types = type.isUnion() ? type.types : [type]
  const values = types.map((item) => item.isStringLiteral() ? item.value : null)
  return values.every((value) => value !== null) && values.length <= 32 ? [...new Set(values)] : null
}

function annotatedCallPatterns(node, initializers, checker, seen) {
  if (!ts.isCallExpression(node) || !ts.isIdentifier(node.expression)) return null

  let symbol = checker.getSymbolAtLocation(node.expression)
  if (!symbol) return null
  if (symbol.flags & ts.SymbolFlags.Alias) symbol = checker.getAliasedSymbol(symbol)

  const declaration = symbol.declarations?.find((item) => ts.isFunctionDeclaration(item))
  if (!declaration || !ts.isFunctionDeclaration(declaration)) return null

  const routePatterns = ts.getJSDocTags(declaration)
    .filter((tag) => tag.tagName.text === 'apiRoute')
    .map((tag) => typeof tag.comment === 'string' ? tag.comment.trim() : '')
    .filter(Boolean)
  if (routePatterns.length === 0) return null

  let patterns = routePatterns
  for (const [index, parameter] of declaration.parameters.entries()) {
    if (!ts.isIdentifier(parameter.name)) continue
    const token = `{${parameter.name.text}}`
    if (!patterns.some((pattern) => pattern.includes(token))) continue

    const argument = node.arguments[index]
    const values = argument
      ? resolveExpression(argument, initializers, checker, new Set(seen)) ?? ['{dynamic}']
      : ['{dynamic}']
    patterns = patterns.flatMap((pattern) => values.map((value) => pattern.split(token).join(value)))
  }
  return [...new Set(patterns)]
}

function resolveExpression(node, initializers, checker, seen = new Set()) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return [node.text]

  const annotatedPatterns = annotatedCallPatterns(node, initializers, checker, seen)
  if (annotatedPatterns) return annotatedPatterns

  if (ts.isTemplateExpression(node)) {
    let patterns = [node.head.text]
    for (const span of node.templateSpans) {
      const next = span.literal.text
      const values = resolveExpression(span.expression, initializers, checker, new Set(seen)) ?? ['{dynamic}']
      patterns = patterns.flatMap((pattern) => values.map((value) => `${pattern}${value}${next}`))
    }
    return patterns
  }

  if (ts.isParenthesizedExpression(node)) return resolveExpression(node.expression, initializers, checker, seen)

  if (ts.isConditionalExpression(node)) {
    const whenTrue = resolveExpression(node.whenTrue, initializers, checker, new Set(seen))
    const whenFalse = resolveExpression(node.whenFalse, initializers, checker, new Set(seen))
    return whenTrue && whenFalse ? [...new Set([...whenTrue, ...whenFalse])] : null
  }

  if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
    const left = resolveExpression(node.left, initializers, checker, new Set(seen))
    const right = resolveExpression(node.right, initializers, checker, new Set(seen))
    if (!left || !right) return null
    return left.flatMap((leftValue) => right.map((rightValue) => leftValue + rightValue))
  }

  if (ts.isIdentifier(node) && initializers.has(node.text) && !seen.has(node.text)) {
    const nextSeen = new Set(seen)
    nextSeen.add(node.text)
    return resolveExpression(initializers.get(node.text), initializers, checker, nextSeen)
  }

  return literalAlternatives(node, checker)
}

function normalizeClientPattern(clientPattern) {
  const queryIndex = clientPattern.indexOf('?')
  const withoutQuery = queryIndex === -1 ? clientPattern : clientPattern.slice(0, queryIndex)
  if (!withoutQuery.startsWith('/')) return null

  const segments = withoutQuery.split('/').slice(1)
  if (segments.some((segment) => segment.includes('{dynamic}') && segment !== '{dynamic}')) return null

  return `/api${withoutQuery}`
}

function routeMatches(candidate, route) {
  const candidateSegments = candidate.split('/')
  const routeSegments = route.split('/')
  if (candidateSegments.length !== routeSegments.length) return false

  return candidateSegments.every((segment, index) => {
    const routeSegment = routeSegments[index]
    const routeParameter = /^\{[^}]+\}$/.test(routeSegment)
    if (segment === '{dynamic}') return routeParameter
    return routeParameter || segment === routeSegment
  })
}

function parseRequestContracts(root) {
  const openAPIPath = path.join(root, 'docs/openapi.json')
  if (!fs.existsSync(openAPIPath)) return []
  const document = JSON.parse(fs.readFileSync(openAPIPath, 'utf8'))
  const schemas = document.components?.schemas ?? {}
  const contracts = []
  for (const [routePath, pathItem] of Object.entries(document.paths ?? {})) {
    for (const [method, operation] of Object.entries(pathItem)) {
      const schema = operation?.requestBody?.content?.['application/json']?.schema
      if (!schema) continue
      const resolved = typeof schema.$ref === 'string'
        ? schemas[schema.$ref.replace('#/components/schemas/', '')]
        : schema
      if (resolved) contracts.push({ method: method.toUpperCase(), path: routePath, schema: resolved })
    }
  }
  return contracts
}

function literalValues(value) {
  if (value.node) {
    if (ts.isStringLiteral(value.node) || ts.isNoSubstitutionTemplateLiteral(value.node)) return [value.node.text]
    if (ts.isNumericLiteral(value.node)) return [Number(value.node.text)]
    if (value.node.kind === ts.SyntaxKind.TrueKeyword) return [true]
    if (value.node.kind === ts.SyntaxKind.FalseKeyword) return [false]
  }
  const types = nonNullableTypes(value.type)
  const values = types.map((item) => {
    if (item.isStringLiteral() || item.isNumberLiteral()) return item.value
    if (item.flags & ts.TypeFlags.BooleanLiteral) return item.intrinsicName === 'true'
    return undefined
  })
  return values.length > 0 && values.every((item) => item !== undefined) ? [...new Set(values)] : null
}

function objectProperties(value, checker) {
  const properties = new Map()
  if (value.node && ts.isObjectLiteralExpression(value.node)) {
    for (const property of value.node.properties) {
      if (ts.isSpreadAssignment(property)) return { properties, complete: false }
      if (ts.isPropertyAssignment(property)) {
        const name = ts.isIdentifier(property.name) || ts.isStringLiteral(property.name)
          ? property.name.text
          : null
        if (name) properties.set(name, { node: property.initializer, type: checker.getTypeAtLocation(property.initializer), optional: false })
      } else if (ts.isShorthandPropertyAssignment(property)) {
        properties.set(property.name.text, { node: property.name, type: checker.getTypeAtLocation(property.name), optional: false })
      }
    }
    return { properties, complete: true }
  }

  for (const symbol of checker.getPropertiesOfType(value.type)) {
    const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0]
    properties.set(symbol.name, {
      node: null,
      type: checker.getTypeOfSymbolAtLocation(symbol, declaration ?? value.node),
      optional: Boolean(symbol.flags & ts.SymbolFlags.Optional),
    })
  }
  return { properties, complete: true }
}

function nonNullableTypes(type) {
  const types = type?.isUnion() ? type.types : type ? [type] : []
  return types.filter((item) => !(item.flags & (ts.TypeFlags.Undefined | ts.TypeFlags.Null)))
}

function primitiveMatches(type, expected, checker) {
  const types = nonNullableTypes(type)
  if (types.length === 0) return false
  return types.every((item) => {
    if (expected === 'string') return Boolean(item.flags & ts.TypeFlags.StringLike)
    if (expected === 'integer' || expected === 'number') return Boolean(item.flags & ts.TypeFlags.NumberLike)
    if (expected === 'boolean') return Boolean(item.flags & ts.TypeFlags.BooleanLike)
    if (expected === 'array') return checker.isArrayType(item) || checker.isTupleType(item)
    if (expected === 'object') return Boolean(item.flags & ts.TypeFlags.Object)
    return true
  })
}

function validateValue(value, schema, checker, field, errors) {
  const expectedTypes = Array.isArray(schema.type) ? schema.type : schema.type ? [schema.type] : []
  const expected = expectedTypes.find((item) => item !== 'null')
  if (expected && !primitiveMatches(value.type, expected, checker)) {
    errors.push(`${field} must be ${expected}`)
    return
  }

  const allowedValues = schema.enum ?? (Object.hasOwn(schema, 'const') ? [schema.const] : null)
  if (allowedValues) {
    const actualValues = literalValues(value)
    if (!actualValues) errors.push(`${field} is not constrained to ${allowedValues.join('|')}`)
    else if (actualValues.some((item) => !allowedValues.includes(item))) {
      errors.push(`${field} allows ${actualValues.join('|')} outside ${allowedValues.join('|')}`)
    }
  }

  if (expected === 'array' && schema.items) {
    if (value.node && ts.isArrayLiteralExpression(value.node)) {
      if (schema.minItems !== undefined && value.node.elements.length < schema.minItems) errors.push(`${field} has fewer than ${schema.minItems} items`)
      if (schema.maxItems !== undefined && value.node.elements.length > schema.maxItems) errors.push(`${field} has more than ${schema.maxItems} items`)
      for (const [index, element] of value.node.elements.entries()) {
        validateValue({ node: element, type: checker.getTypeAtLocation(element), optional: false }, schema.items, checker, `${field}[${index}]`, errors)
      }
    } else {
      for (const type of nonNullableTypes(value.type)) {
        const elementType = checker.getElementTypeOfArrayType(type)
        if (elementType) validateValue({ node: null, type: elementType, optional: false }, schema.items, checker, `${field}[]`, errors)
      }
    }
  }

  if (expected !== 'object') return
  const shape = objectProperties(value, checker)
  if (!shape.complete) errors.push(`${field} contains an unresolved spread`)
  if (schema.maxProperties !== undefined && shape.properties.size > schema.maxProperties) {
    errors.push(`${field} has more than ${schema.maxProperties} properties`)
  }
  for (const required of schema.required ?? []) {
    const property = shape.properties.get(required)
    if (!property) errors.push(`${field}.${required} is missing`)
    else if (property.optional) errors.push(`${field}.${required} is optional in TypeScript`)
  }
  for (const [name, property] of shape.properties) {
    const propertySchema = schema.properties?.[name]
    if (propertySchema) {
      validateValue(property, propertySchema, checker, `${field}.${name}`, errors)
    } else if (schema.additionalProperties === false) {
      errors.push(`${field}.${name} is not allowed`)
    } else if (schema.additionalProperties && typeof schema.additionalProperties === 'object') {
      validateValue(property, schema.additionalProperties, checker, `${field}.${name}`, errors)
    }
  }
}

function validateRequestBody(call, contract, checker) {
  if (!call.body) return ['request body is missing']
  const errors = []
  validateValue({ node: call.body, type: checker.getTypeAtLocation(call.body), optional: false }, contract.schema, checker, 'body', errors)
  return errors
}

function inspectSourceFile(sourceFile, sourceRoot, checker) {
  const filePath = sourceFile.fileName
  const initializers = collectConstInitializers(sourceFile)
  const calls = []

  function visit(node) {
    if (
      ts.isCallExpression(node)
      && ts.isPropertyAccessExpression(node.expression)
      && ts.isIdentifier(node.expression.expression)
      && node.expression.expression.text === 'api'
      && apiMethods.has(node.expression.name.text)
    ) {
      const location = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile))
      const argument = node.arguments[0]
      const patterns = argument ? resolveExpression(argument, initializers, checker) : null
      calls.push({
        method: node.expression.name.text.toUpperCase(),
        file: path.relative(sourceRoot, filePath),
        line: location.line + 1,
        expression: argument?.getText(sourceFile) ?? '<missing>',
        body: node.arguments[1] ?? null,
        bodyExpression: node.arguments[1]?.getText(sourceFile) ?? '<missing>',
        patterns,
      })
    }
    ts.forEachChild(node, visit)
  }

  visit(sourceFile)
  return calls
}

export function verifyFrontendRoutes({
  root,
  write = console.log,
  writeError = console.error,
  reportUnverified = false,
}) {
  const manifestPath = path.join(root, 'internal/api/routes_manifest.go')
  const sourceRoot = path.join(root, 'web/src')
  const routes = parseManifest(fs.readFileSync(manifestPath, 'utf8'))
  const requestContracts = parseRequestContracts(root)
  if (routes.length === 0) throw new Error(`no routes found in ${manifestPath}`)

  const sourceFiles = collectSourceFiles(sourceRoot)
  const configPath = path.join(root, 'web/tsconfig.json')
  let compilerOptions = { jsx: ts.JsxEmit.ReactJSX, module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ESNext }
  if (fs.existsSync(configPath)) {
    const config = ts.readConfigFile(configPath, ts.sys.readFile)
    if (!config.error) compilerOptions = ts.parseJsonConfigFileContent(config.config, ts.sys, path.dirname(configPath)).options
  }
  const program = ts.createProgram(sourceFiles, compilerOptions)
  const checker = program.getTypeChecker()
  const calls = sourceFiles.flatMap((filePath) => {
    const sourceFile = program.getSourceFile(filePath)
    return sourceFile ? inspectSourceFile(sourceFile, sourceRoot, checker) : []
  })
  const failures = []
  const unverified = []
  const payloadFailures = []
  const seenPayloadCalls = new Set()
  const verifiedPayloadCalls = new Set()
  let verified = 0

  for (const call of calls) {
    if (!call.patterns) {
      unverified.push(call)
      continue
    }

    let callVerified = true
    for (const rawPattern of call.patterns) {
      const candidate = normalizeClientPattern(rawPattern)
      if (!candidate) {
        callVerified = false
        unverified.push(call)
        break
      }
      const matchedRoutes = routes.filter((route) => route.method === call.method && routeMatches(candidate, route.path))
      if (matchedRoutes.length === 0) failures.push({ ...call, candidate })
      for (const contract of requestContracts.filter((item) => item.method === call.method && routeMatches(candidate, item.path))) {
        const key = `${call.file}:${call.line} ${contract.method} ${contract.path}`
        if (seenPayloadCalls.has(key)) continue
        seenPayloadCalls.add(key)
        const errors = validateRequestBody(call, contract, checker)
        if (errors.length > 0) payloadFailures.push({ ...call, contract, errors })
        else verifiedPayloadCalls.add(key)
      }
    }
    if (callVerified && !failures.some((failure) => failure.file === call.file && failure.line === call.line)) verified += 1
  }

  if (failures.length > 0 || payloadFailures.length > 0) {
    if (failures.length > 0) {
    writeError('unregistered frontend API routes:')
    for (const failure of failures) {
      writeError(`  ${failure.file}:${failure.line} ${failure.method} ${failure.candidate} (${failure.expression})`)
    }
    }
    if (payloadFailures.length > 0) {
      writeError('frontend API payload contract failures:')
      for (const failure of payloadFailures) {
        writeError(`  ${failure.file}:${failure.line} ${failure.method} ${failure.contract.path} (${failure.bodyExpression})`)
        for (const error of failure.errors) writeError(`    - ${error}`)
      }
    }
    return {
      ok: false,
      total: calls.length,
      verified,
      unverified: unverified.length,
      unverifiedCalls: unverified,
      failures,
      payloadTotal: seenPayloadCalls.size,
      payloadVerified: verifiedPayloadCalls.size,
      payloadFailures,
    }
  }

  write(`frontend API route check passed: ${verified}/${calls.length} statically verified; ${unverified.length} dynamic calls reported but not claimed as verified`)
  write(`frontend API payload check passed: ${verifiedPayloadCalls.size}/${seenPayloadCalls.size} promoted request bodies verified`)
  if (reportUnverified && unverified.length > 0) {
    write('unverified dynamic frontend API calls:')
    for (const call of unverified) {
      write(`  ${call.file}:${call.line} ${call.method} ${call.expression}`)
    }
  }
  return {
    ok: true,
    total: calls.length,
    verified,
    unverified: unverified.length,
    unverifiedCalls: unverified,
    failures: [],
    payloadTotal: seenPayloadCalls.size,
    payloadVerified: verifiedPayloadCalls.size,
    payloadFailures: [],
  }
}

const currentFile = fileURLToPath(import.meta.url)
if (process.argv[1] && path.resolve(process.argv[1]) === currentFile) {
  const defaultRoot = path.resolve(path.dirname(currentFile), '../..')
  const result = verifyFrontendRoutes({
    root: process.env.HSERVER_ROOT || defaultRoot,
    reportUnverified: process.argv.includes('--show-unverified'),
  })
  if (!result.ok) process.exitCode = 1
}
