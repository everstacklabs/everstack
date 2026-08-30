/**
 * Attribute formatting utilities for traces observability
 * Handles attribute name formatting, grouping, and type detection
 */

import { isJsonString } from './trace-formatters'

/**
 * Format attribute key to human-readable title case
 * Converts "llm.tokens.input" → "LLM Tokens Input"
 */
export function formatAttributeName(key: string): string {
  if (!key) return ''

  // Replace dots and underscores with spaces
  const words = key.replace(/[._]/g, ' ').split(' ')

  // Capitalize each word, with special handling for acronyms
  return words
    .map((word) => {
      // Common acronyms to keep uppercase
      const acronyms = ['llm', 'api', 'http', 'url', 'id', 'ai', 'gpu', 'cpu', 'io', 'db', 'sql']
      if (acronyms.includes(word.toLowerCase())) {
        return word.toUpperCase()
      }

      // Capitalize first letter
      return word.charAt(0).toUpperCase() + word.slice(1).toLowerCase()
    })
    .join(' ')
}

/**
 * Group attributes by prefix (e.g., "llm.", "http.", "db.")
 */
export function groupAttributes(attributes: Record<string, string>): Map<string, Record<string, string>> {
  const groups = new Map<string, Record<string, string>>()

  // Define known prefixes and their display names
  const prefixMap: Record<string, string> = {
    // Original prefixes
    'llm.': 'LLM',
    'gen_ai.': 'Gen AI',
    'http.': 'HTTP',
    'db.': 'Database',
    'rpc.': 'RPC',
    'messaging.': 'Messaging',
    'server.': 'Server',
    'client.': 'Client',
    'network.': 'Network',
    'code.': 'Code',
    'enduser.': 'End User',
    'service.': 'Service',
    'telemetry.': 'Telemetry',
    'observation.': 'Observation',

    // New prefixes for enhanced trace data
    'cache.': 'Cache',
    'cost.': 'Cost',
    'model.': 'Model',
    'request.': 'Request',
    'response.': 'Response',
    'resolution.': 'Resolution',
    'normalization.': 'Normalization',
    'fallback.': 'Fallback',
    'latency.': 'Latency',
    'performance.': 'Performance',
    'business.': 'Business',
    'ratelimit.': 'Rate Limit',
    'tokens.': 'Tokens',
    'validation.': 'Validation',
    'trace.': 'Trace',
    'tool_loop.': 'Tool Loop',
    'node.': 'Workflow',
    'step.': 'Step',
    'schema.': 'Schema',
    'correlation.': 'Correlation',
    'deployment.': 'Deployment',
    'instance.': 'Instance',
    'tenant.': 'Tenant',
    'carbon.': 'Carbon',
    'provider.': 'Provider',
    'function.': 'Function',
    'container.': 'Container',
    'span.': 'Span',
  }

  // Create a "General" group for attributes without known prefixes
  const general: Record<string, string> = {}
  groups.set('General', general)

  Object.entries(attributes).forEach(([key, value]) => {
    // Find matching prefix
    let foundPrefix = false
    for (const [prefix, groupName] of Object.entries(prefixMap)) {
      if (key.startsWith(prefix)) {
        if (!groups.has(groupName)) {
          groups.set(groupName, {})
        }
        groups.get(groupName)![key] = value
        foundPrefix = true
        break
      }
    }

    // If no prefix found, add to General
    if (!foundPrefix) {
      general[key] = value
    }
  })

  // Remove empty General group if no attributes were added
  if (Object.keys(general).length === 0) {
    groups.delete('General')
  }

  return groups
}

/**
 * Detect the type of an attribute value
 */
export type AttributeType =
  | 'json'
  | 'url'
  | 'boolean'
  | 'number'
  | 'timestamp'
  | 'uuid'
  | 'hash'
  | 'string'

export function detectAttributeType(value: string): AttributeType {
  if (!value || typeof value !== 'string') {
    return 'string'
  }

  // Check for JSON
  if (isJsonString(value)) {
    return 'json'
  }

  // Check for URL
  try {
    new URL(value)
    return 'url'
  } catch {
    // Not a URL
  }

  // Check for boolean
  if (value === 'true' || value === 'false') {
    return 'boolean'
  }

  // Check for number
  if (!isNaN(Number(value)) && value.trim() !== '') {
    // Check if it could be a timestamp (Unix timestamp in milliseconds)
    const num = Number(value)
    if (num > 1000000000000 && num < 9999999999999) {
      return 'timestamp'
    }
    return 'number'
  }

  // Check for UUID pattern
  if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)) {
    return 'uuid'
  }

  // Check for hash-like strings (hexadecimal, 32+ chars)
  if (/^[0-9a-f]{32,}$/i.test(value)) {
    return 'hash'
  }

  return 'string'
}

/**
 * Format attribute name within a group by stripping the group prefix
 * For cleaner display within grouped sections
 * e.g., "cache.hit" in Cache group becomes "Hit"
 */
export function formatAttributeNameInGroup(key: string, groupPrefix: string): string {
  const withoutPrefix = key.startsWith(groupPrefix)
    ? key.slice(groupPrefix.length)
    : key
  return formatAttributeName(withoutPrefix)
}

/**
 * Get a description for an attribute group
 */
export function getGroupDescription(groupName: string): string {
  const descriptions: Record<string, string> = {
    'LLM': 'Large Language Model related attributes including tokens, models, and prompts',
    'Gen AI': 'Generative AI operation attributes',
    'HTTP': 'HTTP request and response attributes',
    'Database': 'Database operation attributes',
    'RPC': 'Remote Procedure Call attributes',
    'Messaging': 'Message queue and pub/sub attributes',
    'Server': 'Server-side attributes',
    'Client': 'Client-side attributes',
    'Network': 'Network-level attributes',
    'Code': 'Code execution attributes',
    'End User': 'End user identification attributes',
    'Service': 'Service identification and versioning',
    'Telemetry': 'Telemetry SDK attributes',
    'Observation': 'Observability and tracing metadata',
    'General': 'Other attributes',
    // New group descriptions
    'Cache': 'Cache lookup and storage attributes',
    'Cost': 'Cost and pricing attributes',
    'Model': 'Model configuration and metadata',
    'Request': 'Request processing attributes',
    'Response': 'Response processing attributes',
    'Resolution': 'Model and endpoint resolution attributes',
    'Normalization': 'Request normalization attributes',
    'Fallback': 'Fallback configuration attributes',
    'Latency': 'Latency and timing metrics',
    'Performance': 'Performance metrics and indicators',
    'Business': 'Business logic and rules',
    'Rate Limit': 'Rate limiting configuration and status',
    'Tokens': 'Token usage and counts',
    'Validation': 'Request/response validation attributes',
    'Trace': 'Trace identification and correlation',
    'Tool Loop': 'Tool loop iteration attributes',
    'Workflow': 'Workflow node execution attributes',
    'Step': 'Execution step attributes',
    'Schema': 'Schema validation and configuration',
    'Correlation': 'Request correlation attributes',
    'Deployment': 'Deployment configuration attributes',
    'Instance': 'Instance identification attributes',
    'Tenant': 'Multi-tenant identification attributes',
    'Carbon': 'Carbon footprint and sustainability metrics',
    'Provider': 'Provider configuration and metadata',
    'Function': 'Serverless function execution attributes',
    'Container': 'Container execution attributes',
    'Span': 'Span metadata and configuration',
  }

  return descriptions[groupName] || ''
}

/**
 * Check if an attribute should be hidden by default
 * (internal or debug attributes)
 */
export function shouldHideByDefault(key: string): boolean {
  const hidePatterns = [
    /^telemetry\./,
    /^_/,
    /\.internal\./,
    /\.debug\./,
    /^otel\./,
  ]

  return hidePatterns.some(pattern => pattern.test(key))
}

/**
 * Sort attributes within a group by importance/relevance
 */
export function sortAttributeKeys(keys: string[]): string[] {
  // Define priority patterns (higher index = higher priority)
  const priorities: Record<string, number> = {
    'name': 100,
    'model': 90,
    'provider': 85,
    'method': 80,
    'status': 75,
    'code': 70,
    'message': 65,
    'tokens': 60,
    'cost': 55,
    'error': 50,
  }

  return keys.sort((a, b) => {
    // Get priority for each key
    const aPriority = Object.entries(priorities).find(([pattern]) =>
      a.toLowerCase().includes(pattern)
    )?.[1] || 0

    const bPriority = Object.entries(priorities).find(([pattern]) =>
      b.toLowerCase().includes(pattern)
    )?.[1] || 0

    // Higher priority first
    if (aPriority !== bPriority) {
      return bPriority - aPriority
    }

    // Alphabetical if same priority
    return a.localeCompare(b)
  })
}
