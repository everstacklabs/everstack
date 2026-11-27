/**
 * Finds DOM attributes that session replay would record verbatim.
 *
 * rrweb records element attributes as-is, even inside masked regions, and
 * posthog-js has no attribute masking hook. A `title` or `aria-label` fed by
 * customer data (a command, a prompt, an error message) would therefore
 * reach PostHog even though the visible text is masked. Used by
 * replay-attributes.test.ts as a regression guard; not shipped.
 */
import ts from 'typescript'

/** Attributes rrweb records verbatim that could carry customer text or tokens. */
export const RECORDED_TEXT_ATTRIBUTES = new Set(['title', 'aria-label', 'aria-description', 'aria-valuetext', 'alt', 'href'])

/**
 * Capitalized components known to forward these attributes to a DOM
 * element. Any other capitalized component's `title` is a prop it renders
 * as text, which the text mask covers.
 */
const FORWARDING_COMPONENTS = new Set(['Button', 'Badge', 'Link', 'Input', 'Textarea', 'Label', 'Iconify.Icon', 'Icon'])

export type AttributeFinding = { file: string; line: number; attribute: string; expression: string }

function isIntrinsic(tag: string): boolean {
  if (/^[a-z]/.test(tag)) return true
  if (/^motion\.[a-z]/.test(tag)) return true
  return FORWARDING_COMPONENTS.has(tag)
}

/** True when every value the expression can produce is a literal in the source. */
export function isStaticExpression(node: ts.Expression): boolean {
  if (ts.isParenthesizedExpression(node)) return isStaticExpression(node.expression)
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return true
  if (node.kind === ts.SyntaxKind.NullKeyword) return true
  if (ts.isIdentifier(node) && node.text === 'undefined') return true
  if (ts.isConditionalExpression(node)) return isStaticExpression(node.whenTrue) && isStaticExpression(node.whenFalse)
  if (ts.isBinaryExpression(node)) {
    const op = node.operatorToken.kind
    if (op === ts.SyntaxKind.BarBarToken || op === ts.SyntaxKind.QuestionQuestionToken) {
      return isStaticExpression(node.left) && isStaticExpression(node.right)
    }
    if (op === ts.SyntaxKind.AmpersandAmpersandToken) return isStaticExpression(node.right)
  }
  return false
}

/** The element spreads `replayBlock` or sets `data-ph-block`, so replay records none of its attributes. */
function isReplayBlocked(owner: ts.JsxOpeningElement | ts.JsxSelfClosingElement, sf: ts.SourceFile): boolean {
  return owner.attributes.properties.some(
    (attr) =>
      (ts.isJsxSpreadAttribute(attr) && attr.expression.getText(sf) === 'replayBlock') ||
      (ts.isJsxAttribute(attr) && attr.name.getText(sf) === 'data-ph-block'),
  )
}

export function scanSource(file: string, source: string): AttributeFinding[] {
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const findings: AttributeFinding[] = []
  const visit = (node: ts.Node) => {
    if (ts.isJsxAttribute(node)) {
      const name = node.name.getText(sf)
      const owner = node.parent.parent
      if (
        RECORDED_TEXT_ATTRIBUTES.has(name) &&
        (ts.isJsxOpeningElement(owner) || ts.isJsxSelfClosingElement(owner)) &&
        isIntrinsic(owner.tagName.getText(sf)) &&
        !isReplayBlocked(owner, sf) &&
        node.initializer &&
        ts.isJsxExpression(node.initializer) &&
        node.initializer.expression &&
        !isStaticExpression(node.initializer.expression)
      ) {
        findings.push({
          file,
          line: sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1,
          attribute: name,
          expression: node.initializer.expression.getText(sf).replace(/\s+/g, ' '),
        })
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  return findings
}
