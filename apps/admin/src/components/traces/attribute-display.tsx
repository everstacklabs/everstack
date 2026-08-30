/**
 * Smart attribute display component
 * Renders attributes with type-aware formatting
 */

import { useState } from 'react'
import { ui } from '@everstack/ui'
import { Check, Copy, ExternalLink, CheckCircle, XCircle } from 'lucide-react'
import { cn } from '@everstack/utils/functions/cn'
import { copyToClipboard } from '@everstack/utils/functions/clipboard'
import { toast } from '@everstack/ui/components'
import {
  formatAttributeName,
  detectAttributeType,
} from '@/utils/attribute-formatter'
import { safeJsonParse, formatNumber } from '@/utils/trace-formatters'
import { JsonViewer } from '@/ui/json-viewer'
import { statusTint } from './trace-viz'
import {
  ConversationView,
  hasStructuredConversation,
} from './conversation-view'
import dayjs from 'dayjs'

const { Badge, Tooltip } = ui

function isStructuredValue(
  value: unknown,
): value is Record<string, unknown> | unknown[] {
  return value !== null && typeof value === 'object'
}

function stringifyAttributeValue(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  if (
    typeof value === 'number' ||
    typeof value === 'boolean' ||
    typeof value === 'bigint'
  ) {
    return String(value)
  }
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

interface AttributeDisplayProps {
  attrKey: string
  value: unknown
  className?: string
}

export function AttributeDisplay({
  attrKey,
  value,
  className,
}: AttributeDisplayProps) {
  const [copied, setCopied] = useState(false)
  const valueText = stringifyAttributeValue(value)
  const type = isStructuredValue(value)
    ? 'json'
    : detectAttributeType(valueText)
  const formattedName = formatAttributeName(attrKey)
  const isConversationPayload =
    type === 'json' && hasStructuredConversation(valueText)

  const handleCopy = async () => {
    await copyToClipboard(valueText)
    toast.success('Copied to clipboard')
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  const renderValue = () => {
    switch (type) {
      case 'json': {
        if (isConversationPayload) {
          return (
            <div className="max-h-[34rem] overflow-auto rounded border border-brand-main-500/50 bg-brand-main-900/40 p-2.5 light:bg-white/60">
              <ConversationView rawData={valueText} />
            </div>
          )
        }

        const parsed = isStructuredValue(value)
          ? value
          : safeJsonParse<any>(valueText, null)
        if (parsed) {
          return (
            <div className="max-w-full overflow-x-auto">
              <JsonViewer data={parsed} />
            </div>
          )
        }
        return (
          <pre className="text-xs text-brand-main-50 whitespace-pre-wrap break-words light:text-black">
            {valueText}
          </pre>
        )
      }

      case 'url':
        return (
          <a
            href={valueText}
            target="_blank"
            rel="noopener noreferrer"
            className={cn(
              'text-xs underline flex items-center gap-1 break-all',
              statusTint.info.text,
            )}
          >
            {valueText}
            <ExternalLink className="w-3 h-3 flex-shrink-0" />
          </a>
        )

      case 'boolean': {
        const isTrue = valueText === 'true'
        return (
          <div className="flex items-center gap-2">
            {isTrue ? (
              <>
                <CheckCircle
                  className={cn('w-4 h-4', statusTint.success.text)}
                />
                <span className={cn('text-xs', statusTint.success.text)}>
                  True
                </span>
              </>
            ) : (
              <>
                <XCircle className={cn('w-4 h-4', statusTint.error.text)} />
                <span className={cn('text-xs', statusTint.error.text)}>
                  False
                </span>
              </>
            )}
          </div>
        )
      }

      case 'number':
        return (
          <span className="text-xs text-brand-main-50 light:text-black">
            {formatNumber(Number(valueText))}
          </span>
        )

      case 'timestamp': {
        const date = new Date(Number(valueText))
        return (
          <div className="flex flex-col gap-0.5">
            <span className="text-xs text-brand-main-50 light:text-black">
              {dayjs(date).format('MMM D, YYYY HH:mm:ss')}
            </span>
            <span className="text-[10px] text-brand-main-50 light:text-black">
              {dayjs(date).fromNow()}
            </span>
          </div>
        )
      }

      case 'uuid':
        return (
          <span
            className={cn(
              'text-xs break-all',
              statusTint.neutral.text,
            )}
          >
            {valueText}
          </span>
        )

      case 'hash':
        return (
          <Tooltip content={valueText}>
            <span className={cn('text-xs ', statusTint.neutral.text)}>
              {valueText.slice(0, 16)}...
            </span>
          </Tooltip>
        )

      case 'string':
      default:
        return (
          <span className="text-xs text-brand-main-50 whitespace-pre-wrap break-words light:text-black">
            {valueText}
          </span>
        )
    }
  }

  // Inline value types (everything except JSON) sit on the same row as the key
  // for a tight, scannable two-column layout. JSON spans the full width below.
  const isBlock = type === 'json' || isConversationPayload

  return (
    <div
      className={cn(
        'group grid grid-cols-[minmax(0,11rem)_1fr] items-start gap-x-3 gap-y-1 border-b border-brand-main-500/25 py-2',
        className,
      )}
    >
      {/* Key column: formatted name over the raw dotted key. */}
      <div className="min-w-0">
        <div
          className="truncate text-xs font-medium text-brand-main-50 light:text-black"
          title={formattedName}
        >
          {formattedName}
        </div>
        <div
          className="truncate text-[10px] text-brand-main-50 light:text-black"
          title={attrKey}
        >
          {attrKey}
        </div>
      </div>

      {/* Value column with a copy affordance that appears on hover. */}
      <div
        className={cn(
          'flex min-w-0 items-start gap-2',
          isBlock && 'col-span-2 mt-1',
        )}
      >
        <div className="min-w-0 flex-1">{renderValue()}</div>
        <button
          onClick={handleCopy}
          className="shrink-0 rounded p-0.5 text-brand-main-50 opacity-0 transition-opacity hover:text-brand-main-50 group-hover:opacity-100 light:text-black light:hover:text-black"
          title="Copy value"
        >
          {copied ? (
            <Check className={cn('h-3 w-3', statusTint.success.text)} />
          ) : (
            <Copy className="h-3 w-3" />
          )}
        </button>
      </div>
    </div>
  )
}

interface AttributeGroupDisplayProps {
  groupName: string
  attributes: Record<string, unknown>
  description?: string
  defaultExpanded?: boolean
}

export function AttributeGroupDisplay({
  groupName,
  attributes,
  description,
  defaultExpanded = true,
}: AttributeGroupDisplayProps) {
  const [isExpanded, setIsExpanded] = useState(defaultExpanded)

  if (Object.keys(attributes).length === 0) {
    return null
  }

  return (
    <div className="border border-brand-main-500 rounded-lg overflow-hidden">
      {/* Group header */}
      <button
        onClick={() => setIsExpanded(!isExpanded)}
        className="w-full flex items-center justify-between p-3 bg-brand-main-600/20 hover:bg-brand-main-600/30 transition-colors"
      >
        <div className="flex flex-col items-start gap-1">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-brand-main-50 light:text-black">
              {groupName}
            </span>
            <Badge
              variant="outline"
              className="text-[10px] py-0 px-1.5 bg-brand-main-600/20 text-brand-main-50 border-brand-main-500 light:text-black"
            >
              {Object.keys(attributes).length}
            </Badge>
          </div>
          {description && (
            <span className="text-xs text-brand-main-50 light:text-black">
              {description}
            </span>
          )}
        </div>
        <div
          className={cn(
            'transition-transform',
            isExpanded ? 'rotate-180' : 'rotate-0',
          )}
        >
          <svg
            className="w-4 h-4 text-brand-main-50 light:text-black"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M19 9l-7 7-7-7"
            />
          </svg>
        </div>
      </button>

      {/* Group content */}
      {isExpanded && (
        <div className="p-3 bg-brand-main-600/10">
          {Object.entries(attributes).map(([key, value]) => (
            <AttributeDisplay key={key} attrKey={key} value={value} />
          ))}
        </div>
      )}
    </div>
  )
}
