import { useState, useEffect, useRef, forwardRef, useImperativeHandle, useCallback, useMemo } from 'react'
import { Folder, File, ChevronRight, Loader2, Search, FileText } from 'lucide-react'
import { useSandboxFiles, useSandboxFileSearch } from '@/hooks/deployments/use-sandbox'
import { useGitHubRepoTree, useGitHubRepoSearch } from '@/hooks/integrations/use-github'

export interface FileMentionDropdownHandle {
    handleKey: (key: string) => void
}

interface RepoContext {
    installationId: number
    owner: string
    repo: string
    branch?: string
}

interface FileMentionDropdownProps {
    sessionId: string
    isOpen: boolean
    filter: string
    onSelect: (path: string) => void
    onClose: () => void
    /** GitHub repo context — used as primary file source when available */
    repoContext?: RepoContext | null
}

export const FileMentionDropdown = forwardRef<FileMentionDropdownHandle, FileMentionDropdownProps>(
    function FileMentionDropdown({ sessionId, isOpen, filter, onSelect, onClose, repoContext }, ref) {
        const [currentDir, setCurrentDir] = useState('/repo')
        const [selectedIndex, setSelectedIndex] = useState(0)
        const listRef = useRef<HTMLDivElement>(null)

        const isSearchMode = filter.length > 0
        const hasRepo = !!repoContext

        // ── GitHub data sources (primary when repo is configured) ──
        const ghPath = useMemo(() => {
            const dir = currentDir.replace(/^\/(repo|trooper)\/?/, '')
            return dir || undefined
        }, [currentDir])

        const { data: ghFiles, isLoading: isGhBrowseLoading } = useGitHubRepoTree(
            repoContext?.installationId ?? 0,
            repoContext?.owner ?? '',
            repoContext?.repo ?? '',
            {
                ref: repoContext?.branch,
                path: ghPath,
                enabled: isOpen && hasRepo && !isSearchMode,
            }
        )

        const { data: ghSearchResults, isLoading: isGhSearchLoading } = useGitHubRepoSearch(
            repoContext?.installationId ?? 0,
            repoContext?.owner ?? '',
            repoContext?.repo ?? '',
            filter,
            {
                ref: repoContext?.branch,
                enabled: isOpen && hasRepo && isSearchMode,
            }
        )

        // ── Sandbox data sources (used when no repo is configured) ──
        const [sandboxRoot, setSandboxRoot] = useState<'/repo' | '/workspace'>('/repo')
        const [triedTrooper, setTriedTrooper] = useState(false)
        const [sandboxAvailable, setSandboxAvailable] = useState<boolean | null>(null)

        const useSandbox = !hasRepo
        const { data: sbFiles, isLoading: isSbBrowseLoading, error: sbBrowseError } = useSandboxFiles(
            sessionId, currentDir, isOpen && useSandbox && !isSearchMode && sandboxAvailable !== false
        )
        const { data: sbSearchResults, isLoading: isSbSearchLoading, error: sbSearchError } = useSandboxFileSearch(
            sessionId, filter, sandboxRoot, isOpen && useSandbox && isSearchMode && sandboxAvailable !== false
        )

        // Sandbox availability detection (only when no repo context)
        useEffect(() => {
            if (!useSandbox || sandboxAvailable !== null) return
            if (sbFiles && sbFiles.length >= 0 && !sbBrowseError) {
                setSandboxAvailable(true)
            }
        }, [useSandbox, sbFiles, sbBrowseError, sandboxAvailable])

        useEffect(() => {
            if (!useSandbox) return
            if (sbBrowseError && currentDir === '/repo' && !triedTrooper) {
                setTriedTrooper(true)
                setCurrentDir('/workspace')
                setSandboxRoot('/workspace')
            }
        }, [useSandbox, sbBrowseError, currentDir, triedTrooper])

        useEffect(() => {
            if (!useSandbox) return
            if (sbBrowseError && triedTrooper && currentDir === '/workspace') {
                setSandboxAvailable(false)
            }
        }, [useSandbox, sbBrowseError, triedTrooper, currentDir])

        useEffect(() => {
            if (!useSandbox) return
            if (sbSearchError && sandboxRoot === '/repo' && triedTrooper) {
                setSandboxRoot('/workspace')
            }
        }, [useSandbox, sbSearchError, sandboxRoot, triedTrooper])

        // ── Merge & sort results ──
        const browseFiles = useMemo(() => {
        const source = hasRepo ? (ghFiles ?? []) : (sbFiles ?? [])
        return source.slice().sort((a, b) => {
            if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
            return String(a.name ?? '').localeCompare(String(b.name ?? ''))
        })
    }, [hasRepo, ghFiles, sbFiles])

        const searchFiles = useMemo(() => {
        const source = hasRepo ? (ghSearchResults ?? []) : (sbSearchResults ?? [])
        return source.slice().sort((a, b) => {
            if (a.isDir !== b.isDir) return a.isDir ? 1 : -1
            return String(a.name ?? '').localeCompare(String(b.name ?? ''))
        })
    }, [hasRepo, ghSearchResults, sbSearchResults])

        const displayFiles = isSearchMode ? searchFiles : browseFiles
        const isLoading = hasRepo
            ? (isSearchMode ? isGhSearchLoading : isGhBrowseLoading)
            : (isSearchMode ? isSbSearchLoading : isSbBrowseLoading)

        // Reset selection when filter or directory changes
        useEffect(() => {
            setSelectedIndex(0)
        }, [filter, currentDir])

        useEffect(() => {
            if (!listRef.current) return
            const selected = listRef.current.children[selectedIndex] as HTMLElement | undefined
            selected?.scrollIntoView({ block: 'nearest' })
        }, [selectedIndex])

        const navigateUp = useCallback(() => {
            if (currentDir === '/repo' || currentDir === '/workspace') return
            const parent = currentDir.replace(/\/[^/]+$/, '') || '/'
            if (parent.startsWith('/repo') || parent.startsWith('/workspace')) {
                setCurrentDir(parent)
            }
        }, [currentDir])

        const stripRoot = useCallback((p: string) => {
            if (p.startsWith('/workspace/')) return p.slice('/workspace'.length)
            if (p === '/workspace') return '/'
            if (p.startsWith('/repo/')) return p.slice('/repo'.length)
            if (p === '/repo') return '/'
            return p
        }, [])

        const selectItem = useCallback(
            (index: number) => {
                const item = displayFiles[index]
                if (!item) return
                if (item.isDir && !isSearchMode) {
                    // GitHub paths are like /src/foo — normalize to /repo/src/foo for nav
                    const navPath = hasRepo && !item.path.startsWith('/repo')
                        ? '/repo' + item.path
                        : item.path
                    setCurrentDir(navPath)
                } else {
                    onSelect(stripRoot(item.path))
                }
            },
            [displayFiles, isSearchMode, onSelect, stripRoot, hasRepo]
        )

        const handleFreeformSubmit = useCallback(() => {
            if (filter.trim()) {
                onSelect(filter.trim())
            }
        }, [filter, onSelect])

        useImperativeHandle(ref, () => ({
            handleKey(key: string) {
                // Freeform mode: no sandbox AND no repo
                if (!hasRepo && sandboxAvailable === false) {
                    if (key === 'Enter' || key === 'Tab') handleFreeformSubmit()
                    else if (key === 'Escape') onClose()
                    return
                }

                switch (key) {
                    case 'ArrowDown':
                        setSelectedIndex((prev) => Math.min(prev + 1, displayFiles.length - 1))
                        break
                    case 'ArrowUp':
                        setSelectedIndex((prev) => Math.max(prev - 1, 0))
                        break
                    case 'ArrowRight': {
                        const item = displayFiles[selectedIndex]
                        if (item?.isDir && !isSearchMode) selectItem(selectedIndex)
                        break
                    }
                    case 'ArrowLeft':
                        if (!isSearchMode) navigateUp()
                        break
                    case 'Enter':
                    case 'Tab':
                        if (displayFiles.length > 0) selectItem(selectedIndex)
                        else if (isSearchMode) handleFreeformSubmit()
                        break
                    case 'Escape':
                        onClose()
                        break
                    case 'Backspace':
                        if (!filter) navigateUp()
                        break
                }
            },
        }))

        if (!isOpen) return null

        // ── Freeform mode: no repo AND no sandbox ──
        if (!hasRepo && sandboxAvailable === false) {
            return (
                <div className="absolute bottom-full left-0 right-0 mb-1 z-50 bg-brand-main-900 border border-brand-main-600 rounded-lg shadow-xl overflow-hidden">
                    <div className="px-3 py-2.5 space-y-2">
                        <div className="flex items-center gap-2 text-[11px] text-white/50 light:text-black/50">
                            <FileText className="w-3.5 h-3.5 shrink-0" />
                            <span>Type a file path and press <kbd className="px-1 py-0.5 rounded bg-white/10 text-[10px] font-mono light:bg-black/10">Enter</kbd></span>
                        </div>
                        {filter && (
                            <button
                                type="button"
                                onClick={handleFreeformSubmit}
                                className="flex items-center gap-2 w-full px-2 py-1.5 rounded-md bg-brand-main-700/50 hover:bg-brand-main-700/70 transition-colors text-left"
                            >
                                <File className="w-3.5 h-3.5 text-white/40 shrink-0 light:text-black/40" />
                                <span className="text-xs text-white/80 truncate font-mono light:text-black/80">{filter}</span>
                                <span className="ml-auto text-[10px] text-white/30 shrink-0 light:text-black/30">insert</span>
                            </button>
                        )}
                    </div>
                </div>
            )
        }

        // ── Loading initial state (sandbox-only, waiting to detect availability) ──
        if (!hasRepo && sandboxAvailable === null && isSbBrowseLoading) {
            return (
                <div className="absolute bottom-full left-0 right-0 mb-1 z-50 bg-brand-main-900 border border-brand-main-600 rounded-lg shadow-xl overflow-hidden">
                    <div className="flex items-center justify-center gap-2 py-6 text-white/30 light:text-black/30">
                        <Loader2 className="w-4 h-4 animate-spin" />
                        <span className="text-xs">Loading files...</span>
                    </div>
                </div>
            )
        }

        // ── File browser ──
        const rootDir = hasRepo ? '/repo' : (triedTrooper ? '/workspace' : '/repo')
        const allSegments = currentDir.split('/').filter(Boolean)
        const segments = allSegments[0] === 'trooper' || allSegments[0] === 'repo'
            ? allSegments.slice(1)
            : allSegments

        const getRelativePath = (fullPath: string) => {
            // Strip /repo/ or /workspace/ prefix, or just leading /
            const stripped = fullPath.replace(/^\/(repo|trooper)\//, '/').replace(/^\//, '')
            const fileName = fullPath.split('/').pop() ?? ''
            return stripped.replace(new RegExp(`${fileName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`), '')
        }

        const sourceLabel = hasRepo
            ? `${repoContext!.owner}/${repoContext!.repo}`
            : '/'

        return (
            <div className="absolute bottom-full left-0 right-0 mb-1 z-50 bg-brand-main-900 border border-brand-main-600 rounded-lg shadow-xl overflow-hidden">
                {/* Header */}
                {isSearchMode ? (
                    <div className="flex items-center gap-1.5 px-3 py-1.5 border-b border-brand-main-700/50 text-[11px] text-white/40 light:text-black/40">
                        <Search className="w-3 h-3" />
                        <span>Searching for &quot;{filter}&quot;</span>
                        {isLoading && <Loader2 className="w-3 h-3 animate-spin ml-auto" />}
                    </div>
                ) : (
                    <div className="flex items-center gap-1 px-3 py-1.5 border-b border-brand-main-700/50 text-[11px] text-white/40 overflow-x-auto light:text-black/40">
                        <button
                            type="button"
                            onClick={() => setCurrentDir(rootDir)}
                            className="hover:text-white/70 transition-colors shrink-0 light:hover:text-black/70"
                        >
                            {sourceLabel}
                        </button>
                        {segments.map((seg, i) => {
                            const rootPrefix = allSegments[0] !== segments[0] ? '/' + allSegments[0] : ''
                            const segPath = rootPrefix + '/' + segments.slice(0, i + 1).join('/')
                            const isLast = i === segments.length - 1
                            return (
                                <span key={segPath} className="flex items-center gap-1 shrink-0">
                                    <ChevronRight className="w-3 h-3 text-white/20 light:text-black/20" />
                                    {isLast ? (
                                        <span className="text-white/60 light:text-black/60">{seg}</span>
                                    ) : (
                                        <button
                                            type="button"
                                            onClick={() => setCurrentDir(segPath)}
                                            className="hover:text-white/70 transition-colors light:hover:text-black/70"
                                        >
                                            {seg}
                                        </button>
                                    )}
                                </span>
                            )
                        })}
                    </div>
                )}

                {/* File list */}
                <div ref={listRef} className="max-h-[280px] overflow-y-auto">
                    {isLoading && displayFiles.length === 0 && (
                        <div className="flex items-center justify-center gap-2 py-6 text-white/30 light:text-black/30">
                            <Loader2 className="w-4 h-4 animate-spin" />
                            <span className="text-xs">{isSearchMode ? 'Searching...' : 'Loading files...'}</span>
                        </div>
                    )}

                    {!isLoading && displayFiles.length === 0 && (
                        <div className="px-3 py-4 text-xs text-white/30 text-center light:text-black/30">
                            {isSearchMode ? (
                                filter ? (
                                    <button
                                        type="button"
                                        onClick={handleFreeformSubmit}
                                        className="hover:text-white/50 transition-colors light:hover:text-black/50"
                                    >
                                        No matches — press Enter to insert &quot;{filter}&quot;
                                    </button>
                                ) : 'Type to search'
                            ) : 'Empty directory'}
                        </div>
                    )}

                    {displayFiles.map((file, i) => (
                        <button
                            key={file.path}
                            type="button"
                            onClick={() => selectItem(i)}
                            onMouseEnter={() => setSelectedIndex(i)}
                            className={`flex items-center gap-2 w-full px-3 py-1.5 text-left text-sm transition-colors ${
                                i === selectedIndex ? 'bg-brand-main-700/50' : 'hover:bg-brand-main-800/50'
                            }`}
                        >
                            {file.isDir ? (
                                <Folder className="w-3.5 h-3.5 text-blue-400/70 shrink-0" />
                            ) : (
                                <File className="w-3.5 h-3.5 text-white/30 shrink-0 light:text-black/30" />
                            )}
                            {isSearchMode ? (
                                <span className="text-white/80 truncate text-xs light:text-black/80">
                                    <span className="text-white/40 light:text-black/40">{getRelativePath(file.path)}</span>
                                    {file.name}
                                </span>
                            ) : (
                                <span className="text-white/80 truncate text-xs light:text-black/80">{file.name}</span>
                            )}
                            {file.isDir && !isSearchMode && (
                                <ChevronRight className="w-3 h-3 text-white/20 ml-auto shrink-0 light:text-black/20" />
                            )}
                        </button>
                    ))}
                </div>
            </div>
        )
    }
)
