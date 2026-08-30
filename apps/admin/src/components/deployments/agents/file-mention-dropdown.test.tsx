// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRef } from 'react'
import { FileMentionDropdown, type FileMentionDropdownHandle } from './file-mention-dropdown'

// Mock the sandbox files hook
const mockFiles = [
    { name: 'src', path: '/repo/src', size: 0, isDir: true },
    { name: 'package.json', path: '/repo/package.json', size: 1234, isDir: false },
    { name: 'README.md', path: '/repo/README.md', size: 500, isDir: false },
    { name: 'tests', path: '/repo/tests', size: 0, isDir: true },
]

vi.mock('@/hooks/deployments/use-sandbox', () => ({
    useSandboxFiles: vi.fn(() => ({
        data: mockFiles,
        isLoading: false,
        error: null,
    })),
    useSandboxFileSearch: vi.fn((_sessionId: string, query: string) => ({
        data: mockFiles.filter((file) => file.name.toLowerCase().includes(query.toLowerCase())),
        isLoading: false,
        error: null,
    })),
}))

function createWrapper() {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false } },
    })
    return function Wrapper({ children }: { children: React.ReactNode }) {
        return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    }
}

describe('FileMentionDropdown', () => {
    let onSelect: ReturnType<typeof vi.fn>
    let onClose: ReturnType<typeof vi.fn>

    afterEach(cleanup)

    beforeEach(() => {
        Element.prototype.scrollIntoView = vi.fn()
        onSelect = vi.fn()
        onClose = vi.fn()
    })

    it('renders file list from mock data', () => {
        render(
            <FileMentionDropdown
                sessionId="sess-1"
                isOpen={true}
                filter=""
                onSelect={onSelect}
                onClose={onClose}
            />,
            { wrapper: createWrapper() }
        )

        expect(screen.getByText('src')).toBeDefined()
        expect(screen.getByText('package.json')).toBeDefined()
        expect(screen.getByText('README.md')).toBeDefined()
        expect(screen.getByText('tests')).toBeDefined()
    })

    it('sorts directories first', () => {
        render(
            <FileMentionDropdown
                sessionId="sess-1"
                isOpen={true}
                filter=""
                onSelect={onSelect}
                onClose={onClose}
            />,
            { wrapper: createWrapper() }
        )

        const items = screen.getAllByRole('button').filter(
            (btn) => btn.textContent && mockFiles.some((f) => btn.textContent!.includes(f.name))
        )
        // First items should be directories (src, tests)
        expect(items[0].textContent).toContain('src')
        expect(items[1].textContent).toContain('tests')
    })

    it('filter narrows visible items', () => {
        render(
            <FileMentionDropdown
                sessionId="sess-1"
                isOpen={true}
                filter="pack"
                onSelect={onSelect}
                onClose={onClose}
            />,
            { wrapper: createWrapper() }
        )

        expect(screen.getByText('package.json')).toBeDefined()
        expect(screen.queryByText('README.md')).toBeNull()
    })

    it('Enter on file calls onSelect with full path', () => {
        const ref = createRef<FileMentionDropdownHandle>()
        render(
            <FileMentionDropdown
                ref={ref}
                sessionId="sess-1"
                isOpen={true}
                filter="package"
                onSelect={onSelect}
                onClose={onClose}
            />,
            { wrapper: createWrapper() }
        )

        // Only package.json should be visible after filter; Enter selects it
        ref.current!.handleKey('Enter')
        expect(onSelect).toHaveBeenCalledWith('/package.json')
    })

    it('Escape calls onClose', () => {
        const ref = createRef<FileMentionDropdownHandle>()
        render(
            <FileMentionDropdown
                ref={ref}
                sessionId="sess-1"
                isOpen={true}
                filter=""
                onSelect={onSelect}
                onClose={onClose}
            />,
            { wrapper: createWrapper() }
        )

        ref.current!.handleKey('Escape')
        expect(onClose).toHaveBeenCalled()
    })

    it('ArrowDown/ArrowUp moves selection', () => {
        const ref = createRef<FileMentionDropdownHandle>()
        render(
            <FileMentionDropdown
                ref={ref}
                sessionId="sess-1"
                isOpen={true}
                filter=""
                onSelect={onSelect}
                onClose={onClose}
            />,
            { wrapper: createWrapper() }
        )

        // Initial selection is 0 (first dir: src)
        // ArrowDown moves to index 1 (tests dir)
        ref.current!.handleKey('ArrowDown')
        // ArrowDown again moves to index 2 (package.json)
        ref.current!.handleKey('ArrowDown')
        // ArrowUp goes back to index 1 (tests)
        ref.current!.handleKey('ArrowUp')

        // Enter on tests dir should navigate into it (not call onSelect since it's a dir)
        ref.current!.handleKey('Enter')
        expect(onSelect).not.toHaveBeenCalled()
    })

    it('returns null when not open', () => {
        const { container } = render(
            <FileMentionDropdown
                sessionId="sess-1"
                isOpen={false}
                filter=""
                onSelect={onSelect}
                onClose={onClose}
            />,
            { wrapper: createWrapper() }
        )

        expect(container.innerHTML).toBe('')
    })

    it('clicking a file calls onSelect', () => {
        render(
            <FileMentionDropdown
                sessionId="sess-1"
                isOpen={true}
                filter=""
                onSelect={onSelect}
                onClose={onClose}
            />,
            { wrapper: createWrapper() }
        )

        fireEvent.click(screen.getByText('package.json'))
        expect(onSelect).toHaveBeenCalledWith('/package.json')
    })
})
