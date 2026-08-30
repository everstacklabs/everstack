import { ClassificationRulesManager } from './classification-rules-manager'
import { SemanticMappingsManager } from './semantic-mappings-manager'
import { CustomColumnManager } from './custom-column-manager'
import { TraceListSettings } from './trace-list-settings'

/**
 * Thin toolbar row under the ESQL search bar. Filtering now lives entirely in
 * the ESQL bar, so this only hosts the trace-list view controls
 * (classification rules, semantic mappings, custom columns, list settings).
 */
export function TraceToolbar() {
    return (
        <div className="border-b border-brand-main-600 bg-brand-main-950">
            <div className="flex items-center justify-end gap-2 px-2.5 py-1.5">
                <ClassificationRulesManager />
                <SemanticMappingsManager />
                <CustomColumnManager />
                <TraceListSettings />
            </div>
        </div>
    )
}
