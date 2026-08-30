import { ui } from '@everstack/ui'
import { StudioToolbar } from './studio-toolbar'
import { StudioContent } from './studio-content'
import { useStudioKeyboard } from '../hooks/use-studio-keyboard'
import { ChatPanelContent } from '../chat/chat-panel'
import { useExecutionStore } from '@/stores/execution-store'
import { VersionPreviewLayout } from './version-preview-layout'

const { ResizablePanelGroup, ResizablePanel, ResizableHandle } = ui

interface StudioLayoutProps {
    previewVersion?: number | null
}

export function StudioLayout({ previewVersion = null }: StudioLayoutProps) {
    useStudioKeyboard()
    const isTestPanelOpen = useExecutionStore((s) => s.isTestPanelOpen)
    const panelPosition = useExecutionStore((s) => s.panelPosition)

    if (previewVersion !== null) {
        return <VersionPreviewLayout previewVersion={previewVersion} />
    }

    if (!isTestPanelOpen) {
        return (
            <div className="flex h-screen flex-col bg-brand-main-950">
                <StudioToolbar />
                <div className="flex flex-1 h-full overflow-hidden">
                    <div className="flex flex-1 flex-col overflow-hidden">
                        <StudioContent />
                    </div>
                </div>
            </div>
        )
    }

    if (panelPosition === 'bottom') {
        return (
            <div className="flex h-screen flex-col bg-brand-main-950">
                <StudioToolbar />
                <ResizablePanelGroup
                    key="bottom"
                    direction="vertical"
                    className="flex-1 overflow-hidden"
                >
                    <ResizablePanel defaultSize={60} minSize={30}>
                        <div className="flex flex-1 flex-col overflow-hidden h-full">
                            <StudioContent />
                        </div>
                    </ResizablePanel>
                    <ResizableHandle />
                    <ResizablePanel defaultSize={40} minSize={20} maxSize={60}>
                        <ChatPanelContent layout="bottom" />
                    </ResizablePanel>
                </ResizablePanelGroup>
            </div>
        )
    }

    // Right position (default)
    return (
        <div className="flex h-screen flex-col bg-brand-main-950">
            <StudioToolbar />
            <ResizablePanelGroup
                key="right"
                direction="horizontal"
                className="flex-1 overflow-hidden"
            >
                <ResizablePanel defaultSize={70} minSize={40}>
                    <div className="flex flex-1 flex-col overflow-hidden h-full">
                        <StudioContent />
                    </div>
                </ResizablePanel>
                <ResizableHandle />
                <ResizablePanel defaultSize={30} minSize={20} maxSize={50} className="border-l border-brand-main-700">
                    <ChatPanelContent layout="right" />
                </ResizablePanel>
            </ResizablePanelGroup>
        </div>
    )
}
