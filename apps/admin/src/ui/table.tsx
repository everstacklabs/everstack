import { cn } from '@/lib/utils';
import { ArrowUpDown, ArrowUp, ArrowDown, MoreVertical, ui } from '@everstack/ui';
import { Check, Copy, Eye, GripVertical } from 'lucide-react';
import { toast } from '@everstack/ui/components';
import { copyToClipboard } from '@everstack/utils/functions/clipboard';
const { Checkbox, Popover, PopoverContent, PopoverTrigger, Tooltip, TooltipProvider } = ui;
import type { ReactNode } from 'react';
import { memo, useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';

export type SortDirection = 'asc' | 'desc' | null;
export type { ColumnDef, Row } from '@tanstack/react-table';

export type ColumnSizing = 'fixed' | 'content' | 'fluid';
export type CellKind = 'text' | 'code' | 'number' | 'date' | 'status' | 'json' | 'money';
export type CellAlign = 'left' | 'center' | 'right';
export type TableDensity = 'compact' | 'comfortable';
export type ColumnSummary<T = any> = (rows: T[], column: ColumnConfig<T>) => ReactNode | string | number | false | null | undefined;

type ColumnValue = unknown;

export interface ColumnConfig<T = any> {
    id: string;
    header: string;
    width?: string | number;
    defaultWidth?: string | number;
    minWidth?: number;
    maxWidth?: number;
    sizing?: ColumnSizing;
    grow?: number;
    truncate?: boolean;
    align?: CellAlign;
    kind?: CellKind;
    accessor?: keyof T | string;
    render?: (row: T) => ReactNode;
    tooltip?: (row: T, value: ColumnValue) => ReactNode | string | false | null | undefined;
    preview?: (row: T, value: ColumnValue) => ReactNode | false | null | undefined;
    copyValue?: (row: T, value: ColumnValue) => string | false | null | undefined;
    summary?: ColumnSummary<T>;
    headerTooltip?: ReactNode | string;
    className?: string;
    sortable?: boolean;
    resizable?: boolean;
    reorderable?: boolean;
}

export interface RowAction<T = any> {
    label: string;
    icon?: ReactNode;
    onClick: (row: T) => void;
    variant?: 'default' | 'destructive';
    disabled?: (row: T) => boolean;
    disabledReason?: (row: T) => ReactNode | string | undefined;
    hidden?: (row: T) => boolean;
    group?: string;
    shortcut?: string;
}

interface ResponsiveTableProps<T = any> {
    tableId?: string;
    columns: ColumnConfig<T>[];
    data: T[];
    onRowClick?: (row: T) => void;
    isLoading?: boolean;
    isFetching?: boolean;
    emptyMessage?: string | ReactNode;
    minTableWidth?: string;
    rowClassName?: string | ((row: T) => string);
    renderRow?: (row: T, columns: ColumnConfig<T>[]) => ReactNode;
    rowKey?: (row: T) => string | number;
    rowRefs?: React.RefObject<Map<string | number, HTMLTableRowElement | null>>;
    enableSelection?: boolean;
    selectedRows?: Set<string | number>;
    onSelectionChange?: (selected: Set<string | number>) => void;
    sortColumn?: string;
    sortDirection?: SortDirection;
    onSortChange?: (columnId: string, direction: SortDirection) => void;
    rowActions?: RowAction<T>[];
    loadingState?: ReactNode;
    enableResizing?: boolean;
    enableColumnReorder?: boolean;
    enableColumnPersistence?: boolean;
    enableCellTooltips?: boolean;
    enableOverflowTooltips?: boolean;
    enableStickyActions?: boolean;
    forceLeftAlign?: boolean;
    density?: TableDensity;
    enableVirtualization?: boolean;
    estimatedRowHeight?: number;
    onScrollNearEnd?: () => void;
    isLoadingMore?: boolean;
}

const TABLE_STORAGE_PREFIX = 'everstack:table-widths:';
const TABLE_ORDER_STORAGE_PREFIX = 'everstack:table-order:';
const CELL_TOOLTIP_DELAY_MS = 300;

function widthToPx(width: string | number | undefined): number | undefined {
    if (typeof width === 'number' && Number.isFinite(width)) return width;
    if (typeof width !== 'string') return undefined;
    const trimmed = width.trim();
    if (!trimmed.endsWith('px')) return undefined;
    const parsed = Number.parseInt(trimmed.slice(0, -2), 10);
    return Number.isFinite(parsed) ? parsed : undefined;
}

function defaultWidthForColumn(col: Partial<ColumnConfig<any>> & { id: string }): number {
    const configured = widthToPx(col.defaultWidth ?? col.width);
    if (configured) return configured;

    if (col.sizing === 'fixed') return 96;
    if (col.sizing === 'content') return 180;
    if (col.sizing === 'fluid') return 320;

    switch (col.kind) {
        case 'number':
        case 'money':
            return 120;
        case 'date':
            return 170;
        case 'status':
            return 130;
        case 'code':
            return 220;
        case 'json':
            return 280;
        default:
            return 150;
    }
}

function getMinWidth(col: Partial<ColumnConfig<any>> & { id: string }): number {
    if (col.minWidth) return col.minWidth;
    if (col.sizing === 'fixed') return defaultWidthForColumn(col);
    if (col.sizing === 'fluid') return 160;
    if (col.kind === 'status') return 96;
    if (col.kind === 'number' || col.kind === 'money') return 88;
    return 80;
}

function getMaxWidth(col: Partial<ColumnConfig<any>> & { id: string }): number | undefined {
    if (col.maxWidth) return col.maxWidth;
    if (col.sizing === 'fixed') return defaultWidthForColumn(col);
    return undefined;
}

// Helper: Get column width as px string
function getColumnWidth(col: Partial<ColumnConfig<any>> & { id: string }, columnWidths: Map<string, string>): string {
    const stateWidth = columnWidths.get(col.id);
    if (stateWidth) return stateWidth;
    return `${defaultWidthForColumn(col)}px`;
}

// Helper: Get column width in pixels
function getColumnWidthPx(col: Partial<ColumnConfig<any>> & { id: string }, columnWidths: Map<string, string>): number {
    const widthStr = getColumnWidth(col, columnWidths);
    let n = widthToPx(widthStr) ?? defaultWidthForColumn(col);
    const maxWidth = getMaxWidth(col);
    n = Math.max(n, getMinWidth(col));
    if (maxWidth) n = Math.min(n, maxWidth);
    return n;
}

function loadPersistedWidths(tableId: string | undefined): Map<string, string> | null {
    if (!tableId || typeof window === 'undefined') return null;
    try {
        const raw = window.localStorage.getItem(`${TABLE_STORAGE_PREFIX}${tableId}`);
        if (!raw) return null;
        const parsed = JSON.parse(raw);
        if (!parsed || typeof parsed !== 'object') return null;
        return new Map(
            Object.entries(parsed)
                .filter(([, value]) => typeof value === 'string')
                .map(([key, value]) => [key, value as string]),
        );
    } catch {
        return null;
    }
}

function persistWidths(tableId: string | undefined, widths: Map<string, string>) {
    if (!tableId || typeof window === 'undefined') return;
    try {
        window.localStorage.setItem(
            `${TABLE_STORAGE_PREFIX}${tableId}`,
            JSON.stringify(Object.fromEntries(widths)),
        );
    } catch {
        // localStorage can be unavailable in private windows; resizing should still work.
    }
}

function loadPersistedOrder(tableId: string | undefined): string[] | null {
    if (!tableId || typeof window === 'undefined') return null;
    try {
        const raw = window.localStorage.getItem(`${TABLE_ORDER_STORAGE_PREFIX}${tableId}`);
        if (!raw) return null;
        const parsed = JSON.parse(raw);
        if (!Array.isArray(parsed)) return null;
        return parsed.filter((id): id is string => typeof id === 'string' && id.length > 0);
    } catch {
        return null;
    }
}

function persistOrder(tableId: string | undefined, order: string[]) {
    if (!tableId || typeof window === 'undefined') return;
    try {
        window.localStorage.setItem(`${TABLE_ORDER_STORAGE_PREFIX}${tableId}`, JSON.stringify(order));
    } catch {
        // localStorage can be unavailable in private windows; reordering should still work.
    }
}

function reconcileColumnOrder<T>(columns: ColumnConfig<T>[], order: string[] | null | undefined): string[] {
    const columnIds = columns.map((column) => column.id);
    if (!order || order.length === 0) return columnIds;

    const available = new Set(columnIds);
    const ordered = order.filter((id) => available.has(id));
    const seen = new Set(ordered);
    for (const id of columnIds) {
        if (!seen.has(id)) ordered.push(id);
    }
    return ordered;
}

function orderColumns<T>(columns: ColumnConfig<T>[], order: string[]): ColumnConfig<T>[] {
    const byId = new Map(columns.map((column) => [column.id, column]));
    const ordered = order.map((id) => byId.get(id)).filter((column): column is ColumnConfig<T> => !!column);
    const seen = new Set(ordered.map((column) => column.id));
    return [
        ...ordered,
        ...columns.filter((column) => !seen.has(column.id)),
    ];
}

function getCellValue<T>(row: T, column: ColumnConfig<T>): unknown {
    if (!column.accessor) return undefined;
    return (row as any)[column.accessor];
}

function stringifyValue(value: unknown): string {
    if (value === null || value === undefined) return '';
    if (typeof value === 'string') return value;
    if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') {
        return String(value);
    }
    try {
        return JSON.stringify(value, null, 2);
    } catch {
        return String(value);
    }
}

function alignClass(align?: CellAlign) {
    if (align === 'right') return 'justify-end text-right';
    if (align === 'center') return 'justify-center text-center';
    return 'justify-start text-left';
}

function kindClass(kind?: CellKind) {
    if (kind === 'code' || kind === 'json') return 'font-mono text-xs';
    if (kind === 'number' || kind === 'money') return 'font-mono tabular-nums';
    if (kind === 'date') return 'font-mono text-xs tabular-nums';
    return '';
}

function DataCellInner<T>({
    row,
    column,
    density,
    enableCellTooltips,
    enableOverflowTooltips,
    forceLeftAlign,
}: {
    row: T;
    column: ColumnConfig<T>;
    density: TableDensity;
    enableCellTooltips: boolean;
    enableOverflowTooltips: boolean;
    forceLeftAlign: boolean;
}) {
    const value = getCellValue(row, column);
    const content = column.render ? column.render(row) : stringifyValue(value);
    const [copied, setCopied] = useState(false);
    const [isTooltipArmed, setIsTooltipArmed] = useState(false);
    const [isOverflowing, setIsOverflowing] = useState(false);
    const [overflowText, setOverflowText] = useState('');
    const contentRef = useRef<HTMLDivElement>(null);
    const tooltipDelayRef = useRef<number | null>(null);
    const explicitTooltip = enableCellTooltips && isTooltipArmed ? column.tooltip?.(row, value) : undefined;
    const shouldMeasureOverflow =
        enableCellTooltips &&
        isTooltipArmed &&
        enableOverflowTooltips &&
        explicitTooltip !== false &&
        (explicitTooltip === undefined || explicitTooltip === null);

    useEffect(() => {
        if (enableCellTooltips) return;
        if (tooltipDelayRef.current !== null) {
            window.clearTimeout(tooltipDelayRef.current);
            tooltipDelayRef.current = null;
        }
        setIsTooltipArmed(false);
    }, [enableCellTooltips]);

    useEffect(() => {
        return () => {
            if (tooltipDelayRef.current !== null) {
                window.clearTimeout(tooltipDelayRef.current);
            }
        };
    }, []);

    useEffect(() => {
        if (!shouldMeasureOverflow) {
            setIsOverflowing(false);
            setOverflowText('');
            return;
        }
        const el = contentRef.current;
        if (!el) return;
        const update = () => {
            const overflowing = el.scrollWidth > el.clientWidth + 1 || el.scrollHeight > el.clientHeight + 1;
            setIsOverflowing(overflowing);
            setOverflowText(overflowing ? el.textContent?.trim() ?? '' : '');
        };
        update();
        const ro = new ResizeObserver(update);
        ro.observe(el);
        return () => ro.disconnect();
    }, [content, column.id, shouldMeasureOverflow]);

    const fallbackTooltip = shouldMeasureOverflow && isOverflowing ? stringifyValue(value) || overflowText : undefined;
    const tooltipContent = enableCellTooltips
        ? explicitTooltip === false
            ? undefined
            : explicitTooltip ?? fallbackTooltip
        : undefined;
    const preview = column.preview?.(row, value);
    const copyText = column.copyValue
        ? column.copyValue(row, value)
        : column.kind === 'code' || column.kind === 'json'
            ? stringifyValue(value)
            : undefined;
    const showTools = !!preview || !!copyText;
    const truncate = column.truncate !== false;

    const handleCopy = async (e: React.MouseEvent<HTMLButtonElement>) => {
        e.stopPropagation();
        if (!copyText) return;
        await copyToClipboard(copyText);
        toast.success('Copied to clipboard');
        setCopied(true);
        setTimeout(() => setCopied(false), 1200);
    };

    const scheduleTooltip = () => {
        if (!enableCellTooltips) return;
        if (tooltipDelayRef.current !== null) {
            window.clearTimeout(tooltipDelayRef.current);
        }
        tooltipDelayRef.current = window.setTimeout(() => {
            tooltipDelayRef.current = null;
            setIsTooltipArmed(true);
        }, CELL_TOOLTIP_DELAY_MS);
    };

    const dismissTooltip = () => {
        if (tooltipDelayRef.current !== null) {
            window.clearTimeout(tooltipDelayRef.current);
            tooltipDelayRef.current = null;
        }
        setIsTooltipArmed(false);
    };

    const contentNode = (
        <div
            ref={contentRef}
            className={cn(
                'min-w-0 max-w-full',
                truncate ? 'w-fit truncate' : 'whitespace-normal break-words',
                kindClass(column.kind),
            )}
        >
            {content}
        </div>
    );

    const body = (
        <div
            onPointerEnter={scheduleTooltip}
            onPointerLeave={dismissTooltip}
            onPointerDown={dismissTooltip}
            className={cn(
                'flex min-w-0 flex-1',
                !forceLeftAlign && column.align === 'right' && 'justify-end',
                !forceLeftAlign && column.align === 'center' && 'justify-center',
            )}
        >
            {tooltipContent ? (
                <Tooltip content={tooltipContent} side="top" align="center" forceOpen={isTooltipArmed}>
                    {contentNode}
                </Tooltip>
            ) : contentNode}
        </div>
    );

    return (
        <div
            className={cn(
                'group/cell flex min-w-0 items-center gap-2 overflow-hidden text-sm text-brand-main-50 light:text-black/90',
                density === 'compact' ? 'px-3 py-1.5' : 'px-4 py-2',
                forceLeftAlign ? alignClass('left') : alignClass(column.align),
                column.className,
            )}
        >
            {body}

            {showTools && (
                <div className="ml-auto flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover/cell:opacity-100 focus-within:opacity-100">
                    {preview && (
                        <Popover>
                            <PopoverTrigger asChild>
                                <button
                                    type="button"
                                    aria-label="Preview cell"
                                    title="Preview"
                                    onClick={(e) => e.stopPropagation()}
                                    className="rounded border border-brand-main-600 bg-brand-main-800/70 p-1 text-white/45 transition-colors hover:border-brand-secondary-500/50 hover:text-brand-secondary-300 light:text-black/45"
                                >
                                    <Eye className="size-3" />
                                </button>
                            </PopoverTrigger>
                            <PopoverContent
                                align="end"
                                side="bottom"
                                sideOffset={6}
                                className="max-h-96 w-96 overflow-auto border-brand-main-600 bg-brand-main-900 p-3 text-xs text-white/80 shadow-xl light:text-black/80"
                                onClick={(e) => e.stopPropagation()}
                            >
                                {preview}
                            </PopoverContent>
                        </Popover>
                    )}
                    {copyText && (
                        <button
                            type="button"
                            aria-label={copied ? 'Copied cell value' : 'Copy cell value'}
                            title={copied ? 'Copied' : 'Copy'}
                            onClick={handleCopy}
                            className="rounded border border-brand-main-600 bg-brand-main-800/70 p-1 text-white/45 transition-colors hover:border-brand-secondary-500/50 hover:text-brand-secondary-300 light:text-black/45"
                        >
                            {copied ? <Check className="size-3 text-emerald-400" /> : <Copy className="size-3" />}
                        </button>
                    )}
                </div>
            )}
        </div>
    );
}

const DataCell = memo(DataCellInner) as typeof DataCellInner;

export function ResponsiveTable<T = any>({
    tableId,
    columns,
    data,
    onRowClick,
    isLoading = false,
    emptyMessage = 'No data found.',
    minTableWidth = '1600px',
    rowClassName = '',
    renderRow,
    rowKey,
    rowRefs,
    enableSelection = false,
    selectedRows = new Set(),
    onSelectionChange,
    sortColumn,
    sortDirection,
    onSortChange,
    rowActions,
    enableResizing = true,
    enableColumnReorder = false,
    enableColumnPersistence = true,
    enableCellTooltips = false,
    enableOverflowTooltips = false,
    enableStickyActions = true,
    forceLeftAlign = false,
    density = 'comfortable',
    enableVirtualization = false,
    estimatedRowHeight = 40,
    onScrollNearEnd,
    isLoadingMore = false,
    loadingState,
}: ResponsiveTableProps<T>) {
    const [internalSortColumn, setInternalSortColumn] = useState<string>();
    const [internalSortDirection, setInternalSortDirection] = useState<SortDirection>(null);
    const effectiveSortColumn = sortColumn ?? internalSortColumn;
    const effectiveSortDirection = sortDirection ?? internalSortDirection;
    const storageTableId = enableColumnPersistence ? tableId : undefined;
    const [draggingColumn, setDraggingColumn] = useState<string | null>(null);
    const [dragOverColumn, setDragOverColumn] = useState<string | null>(null);

    // Column widths for resizing
    const [columnWidths, setColumnWidths] = useState<Map<string, string>>(
        () => loadPersistedWidths(storageTableId) ?? new Map(columns.map(col => [col.id, getColumnWidth(col, new Map())]))
    );

    // Track which columns have been manually resized
    const [manuallyResizedColumns, setManuallyResizedColumns] = useState<Set<string>>(
        () => new Set(loadPersistedWidths(storageTableId)?.keys() ?? []),
    );
    const [columnOrder, setColumnOrder] = useState<string[]>(
        () => reconcileColumnOrder(columns, loadPersistedOrder(storageTableId)),
    );

    // Resizing state
    const [resizingColumn, setResizingColumn] = useState<string | null>(null);
    const resizeStartX = useRef(0);
    const resizeStartWidth = useRef(0);

    // Container width tracking for auto-stretch
    const viewportRef = useRef<HTMLDivElement>(null);
    const headerRef = useRef<HTMLDivElement>(null);
    const [containerWidth, setContainerWidth] = useState(0);

    // Update column widths when columns change
    useEffect(() => {
        const persisted = loadPersistedWidths(storageTableId);
        setColumnWidths(new Map(columns.map((col) => [col.id, persisted?.get(col.id) ?? getColumnWidth(col, new Map())])));
        setManuallyResizedColumns(new Set(persisted?.keys() ?? []));
    }, [columns, storageTableId]);

    useEffect(() => {
        setColumnOrder((current) => reconcileColumnOrder(columns, loadPersistedOrder(storageTableId) ?? current));
    }, [columns, storageTableId]);

    useEffect(() => {
        if (!storageTableId) return;
        if (manuallyResizedColumns.size === 0) {
            if (typeof window !== 'undefined') {
                try {
                    window.localStorage.removeItem(`${TABLE_STORAGE_PREFIX}${storageTableId}`);
                } catch {
                    // localStorage can be unavailable in private windows.
                }
            }
            return;
        }
        persistWidths(storageTableId, columnWidths);
    }, [columnWidths, manuallyResizedColumns, storageTableId]);

    useEffect(() => {
        if (!storageTableId || !enableColumnReorder) return;
        persistOrder(storageTableId, reconcileColumnOrder(columns, columnOrder));
    }, [columnOrder, columns, enableColumnReorder, storageTableId]);

    // Track container width
    useEffect(() => {
        const el = viewportRef.current;
        if (!el) return;

        const ro = new ResizeObserver(() => setContainerWidth(el.clientWidth || 0));
        ro.observe(el);
        setContainerWidth(el.clientWidth || 0);

        return () => ro.disconnect();
    }, []);

    // Set up virtualizer for row virtualization
    const rowVirtualizer = useVirtualizer({
        count: enableVirtualization ? data.length : 0,
        getScrollElement: () => viewportRef.current,
        estimateSize: () => estimatedRowHeight,
        overscan: 5,
        enabled: enableVirtualization,
    });

    // Scroll to selected row when it changes
    useEffect(() => {
        if (!enableVirtualization || !rowKey) return;

        const selectedKey = Array.from(selectedRows)[0];
        if (!selectedKey) return;

        const selectedIndex = data.findIndex(row => {
            const key = rowKey(row);
            return key === selectedKey;
        });

        if (selectedIndex >= 0) {
            rowVirtualizer.scrollToIndex(selectedIndex, {
                align: 'auto',
                behavior: 'smooth',
            });
        }
    }, [selectedRows, data, enableVirtualization, rowKey, rowVirtualizer]);

    // Detect when user scrolls near the end for infinite scroll
    useEffect(() => {
        if (!enableVirtualization || !onScrollNearEnd || data.length === 0) return;

        const virtualItems = rowVirtualizer.getVirtualItems();
        if (virtualItems.length === 0) return;

        const lastVirtualItem = virtualItems[virtualItems.length - 1];
        const threshold = 10; // Trigger when within last 10 items

        // Check if we're near the end
        if (lastVirtualItem && lastVirtualItem.index >= data.length - threshold) {
            onScrollNearEnd();
        }
    }, [rowVirtualizer.getVirtualItems(), data.length, enableVirtualization, onScrollNearEnd]);


    const orderedColumns = useMemo(
        () => enableColumnReorder ? orderColumns(columns, columnOrder) : columns,
        [columns, columnOrder, enableColumnReorder],
    );

    // Build all columns (selection + data + actions)
    const allColumns = useMemo(
        () => [
            ...(enableSelection
                ? [{ id: '__selection', header: '', width: 40, minWidth: 40, maxWidth: 40, sizing: 'fixed' as const, resizable: false }]
                : []),
            ...orderedColumns,
            ...(rowActions?.length
                ? [{ id: '__actions', header: '', width: 56, minWidth: 56, maxWidth: 56, sizing: 'fixed' as const, resizable: false }]
                : []),
        ],
        [orderedColumns, enableSelection, rowActions?.length],
    );
    const showSummaryRow = orderedColumns.some((column) => !!column.summary);
    const columnSummaries = useMemo(() => {
        const summaries = new Map<string, ReactNode | string | number | false | null | undefined>();
        if (!showSummaryRow) return summaries;
        for (const column of orderedColumns) {
            summaries.set(column.id, column.summary?.(data, column));
        }
        return summaries;
    }, [data, orderedColumns, showSummaryRow]);

    // Calculate total desired width and separate manually resized columns
    const manuallyResizedTotal = allColumns
        .filter(col => manuallyResizedColumns.has(col.id))
        .reduce((sum, col) => sum + getColumnWidthPx(col, columnWidths), 0);

    const autoColumns = allColumns.filter(col => !manuallyResizedColumns.has(col.id));
    const autoColumnsTotal = autoColumns.reduce((sum, col) => sum + getColumnWidthPx(col, columnWidths), 0);

    const totalDesired = manuallyResizedTotal + autoColumnsTotal;
    const shouldStretchToFill = totalDesired > 0 && containerWidth > 0 && totalDesired < containerWidth;

    // Generate grid template - use flexible units when stretching to fill
    const gridTemplate = allColumns.map((col) => {
        const widthPx = getColumnWidthPx(col, columnWidths);
        const minWidth = getMinWidth(col);
        const maxWidth = getMaxWidth(col);

        // If column has fixed max width (like actions), keep it fixed
        if ((col as ColumnConfig<T>).sizing === 'fixed' || (maxWidth && maxWidth === widthPx)) {
            return `${widthPx}px`;
        }

        // Manually resized columns keep their pixel width
        if (manuallyResizedColumns.has(col.id)) {
            return `${widthPx}px`;
        }

        // Use minmax with 1fr to allow columns to grow when stretching
        if (shouldStretchToFill && !maxWidth) {
            const grow = (col as ColumnConfig<T>).grow ?? ((col as ColumnConfig<T>).sizing === 'fluid' ? 2 : 1);
            return `minmax(${minWidth}px, ${grow}fr)`;
        }

        return `${widthPx}px`;
    }).join(' ');

    // Sorting
    const handleSort = useCallback((columnId: string) => {
        const column = columns.find(col => col.id === columnId);
        if (!column?.sortable && columnId !== 'time') return;

        const currentDir = (sortColumn === columnId ? sortDirection : undefined)
            ?? (internalSortColumn === columnId ? internalSortDirection : null);

        let newDirection: SortDirection = 'asc';
        if (currentDir === 'asc') newDirection = 'desc';
        else if (currentDir === 'desc') newDirection = null;

        setInternalSortColumn(newDirection ? columnId : undefined);
        setInternalSortDirection(newDirection);
        onSortChange?.(columnId, newDirection);
    }, [columns, sortColumn, sortDirection, onSortChange, internalSortColumn, internalSortDirection]);

    // Selection
    const handleSelectAll = useCallback(() => {
        if (!onSelectionChange) return;
        if (selectedRows.size === data.length) {
            onSelectionChange(new Set());
        } else {
            onSelectionChange(new Set(data.map((row, idx) => rowKey ? rowKey(row) : idx)));
        }
    }, [data, selectedRows, onSelectionChange, rowKey]);

    const handleSelectRow = useCallback((key: string | number) => {
        if (!onSelectionChange) return;
        const newSelection = new Set(selectedRows);
        if (newSelection.has(key)) {
            newSelection.delete(key);
        } else {
            newSelection.add(key);
        }
        onSelectionChange(newSelection);
    }, [selectedRows, onSelectionChange]);

    const isColumnReorderable = useCallback((column: Partial<ColumnConfig<T>> & { id: string }) => {
        if (!enableColumnReorder) return false;
        if (column.id === '__selection' || column.id === '__actions') return false;
        return (column as ColumnConfig<T>).reorderable !== false;
    }, [enableColumnReorder]);

    const moveColumn = useCallback((sourceId: string, targetId: string) => {
        if (!sourceId || !targetId || sourceId === targetId) return;
        const source = columns.find((column) => column.id === sourceId);
        const target = columns.find((column) => column.id === targetId);
        if (!source || !target || source.reorderable === false || target.reorderable === false) return;

        setColumnOrder((current) => {
            const next = reconcileColumnOrder(columns, current);
            const from = next.indexOf(sourceId);
            const to = next.indexOf(targetId);
            if (from < 0 || to < 0) return current;

            next.splice(from, 1);
            next.splice(to, 0, sourceId);
            return next;
        });
    }, [columns]);

    const handleColumnDragStart = useCallback((e: React.DragEvent, columnId: string) => {
        e.stopPropagation();
        setDraggingColumn(columnId);
        setDragOverColumn(null);
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', columnId);
    }, []);

    const handleColumnDragOver = useCallback((e: React.DragEvent, columnId: string) => {
        if (!draggingColumn || draggingColumn === columnId) return;
        const target = allColumns.find((column) => column.id === columnId);
        if (!target || !isColumnReorderable(target)) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        setDragOverColumn(columnId);
    }, [allColumns, draggingColumn, isColumnReorderable]);

    const handleColumnDrop = useCallback((e: React.DragEvent, columnId: string) => {
        const sourceId = draggingColumn || e.dataTransfer.getData('text/plain');
        if (!sourceId) return;
        e.preventDefault();
        e.stopPropagation();
        moveColumn(sourceId, columnId);
        setDraggingColumn(null);
        setDragOverColumn(null);
    }, [draggingColumn, moveColumn]);

    const handleColumnDragEnd = useCallback(() => {
        setDraggingColumn(null);
        setDragOverColumn(null);
    }, []);

    // Resizing
    const handleResizeStart = useCallback((e: React.MouseEvent, columnId: string) => {
        e.preventDefault();
        e.stopPropagation();

        // Find the actual column from allColumns to get proper width
        const column = allColumns.find(col => col.id === columnId);
        if (!column) return;

        setResizingColumn(columnId);
        resizeStartX.current = e.clientX;

        // Get the actual rendered width from the DOM element
        const headerElement = (e.target as HTMLElement).closest('[data-column-id]') as HTMLElement;
        if (headerElement) {
            resizeStartWidth.current = headerElement.offsetWidth;
        } else {
            // Fallback to calculated width
            const currentWidth = columnWidths.get(columnId);
            if (currentWidth && currentWidth.endsWith('px')) {
                resizeStartWidth.current = parseInt(currentWidth.replace('px', ''), 10);
            } else {
                resizeStartWidth.current = getColumnWidthPx(column, columnWidths);
            }
        }
    }, [columnWidths, allColumns]);

    useEffect(() => {
        if (!resizingColumn) return;

        const handleMouseMove = (e: MouseEvent) => {
            const column = allColumns.find(col => col.id === resizingColumn);
            if (!column) return;

            let newWidth = resizeStartWidth.current + (e.clientX - resizeStartX.current);

            // Apply min/max constraints
            const minWidth = getMinWidth(column);
            const maxWidth = getMaxWidth(column);

            newWidth = Math.max(newWidth, minWidth);
            if (maxWidth) newWidth = Math.min(newWidth, maxWidth);

            setColumnWidths(prev => {
                const updated = new Map(prev);
                updated.set(resizingColumn, `${newWidth}px`);
                return updated;
            });

            // Mark this column as manually resized
            setManuallyResizedColumns(prev => new Set(prev).add(resizingColumn));
        };

        const handleMouseUp = () => setResizingColumn(null);

        document.addEventListener('mousemove', handleMouseMove);
        document.addEventListener('mouseup', handleMouseUp);
        return () => {
            document.removeEventListener('mousemove', handleMouseMove);
            document.removeEventListener('mouseup', handleMouseUp);
        };
    }, [resizingColumn, allColumns]);

    const handleResizeReset = useCallback((e: React.MouseEvent, columnId: string) => {
        e.preventDefault();
        e.stopPropagation();
        const column = allColumns.find(col => col.id === columnId);
        if (!column) return;

        setColumnWidths(prev => {
            const updated = new Map(prev);
            updated.set(columnId, `${defaultWidthForColumn(column)}px`);
            return updated;
        });
        setManuallyResizedColumns(prev => {
            const updated = new Set(prev);
            updated.delete(columnId);
            return updated;
        });
    }, [allColumns]);

    const isAllSelected = data.length > 0 && selectedRows.size === data.length;
    const isSomeSelected = selectedRows.size > 0 && selectedRows.size < data.length;

    // Calculate explicit table width to ensure header and body are perfectly aligned
    const calculatedTableWidth = allColumns.reduce((sum, col) => {
        return sum + getColumnWidthPx(col, columnWidths);
    }, 0);

    const wrapperStyle: React.CSSProperties = shouldStretchToFill
        ? { width: '100%' }
        : { width: `${calculatedTableWidth}px`, minWidth: minTableWidth };

    // Ensure grid has explicit width for perfect alignment
    const gridStyle: React.CSSProperties = {
        gridTemplateColumns: gridTemplate,
        width: shouldStretchToFill ? '100%' : `${calculatedTableWidth}px`,
    };
    const stateMessage = data.length === 0 ? (isLoading ? loadingState : emptyMessage) : null;

    return (
        <TooltipProvider>
        <div className='w-full h-full min-h-0 flex flex-col overflow-hidden'>
            <div className="relative min-h-0 flex-1 overflow-hidden">
            {/* Scrollable container with sticky header */}
            <div ref={viewportRef} className='h-full overflow-auto scrollbar-macos'>
                <div style={wrapperStyle}>
                    {/* Header - Sticky */}
                    <div ref={headerRef} className="sticky top-0 z-10 bg-brand-main-950">
                        <div className='grid border-b border-brand-main-600' style={gridStyle}>
                            {allColumns.map((column) => {
                                const isSpecialCol = column.id === '__selection' || column.id === '__actions';
                                const isSorted = effectiveSortColumn === column.id;
                                const isSortable = !isSpecialCol && ((column as ColumnConfig<T>).sortable || column.id === 'time');
                                const isReorderable = isColumnReorderable(column);
                                const isDragging = draggingColumn === column.id;
                                const isDropTarget = dragOverColumn === column.id;
                                const headerLabel = (
                                    <span className="truncate">{column.header}</span>
                                );

                                return (
                                    <div
                                        key={column.id}
                                        data-column-id={column.id}
                                        className={cn(
                                            'group/header relative flex min-w-0 items-center overflow-hidden border-r border-brand-main-600 bg-brand-main-950 px-4 py-2 text-sm font-medium text-white/90 light:text-black/90',
                                            isSortable && 'cursor-pointer hover:bg-brand-main-900',
                                            isDragging && 'opacity-45',
                                            isDropTarget && 'bg-brand-secondary-500/10 ring-1 ring-inset ring-brand-secondary-400/50',
                                            column.id === '__selection' && 'border-r-0',
                                            column.id === '__actions' && enableStickyActions && 'sticky right-0 z-20 border-l border-brand-main-600 shadow-[-10px_0_18px_-16px_rgba(0,0,0,0.8)]',
                                            (column as ColumnConfig<T>).className,
                                        )}
                                        onClick={() => isSortable && handleSort(column.id)}
                                        onDragOver={(e) => isReorderable && handleColumnDragOver(e, column.id)}
                                        onDragLeave={() => dragOverColumn === column.id && setDragOverColumn(null)}
                                        onDrop={(e) => isReorderable && handleColumnDrop(e, column.id)}
                                    >
                                        {column.id === '__selection' ? (
                                            <Checkbox
                                                checked={isSomeSelected ? "indeterminate" : isAllSelected}
                                                onCheckedChange={handleSelectAll}
                                                onClick={(e) => e.stopPropagation()}

                                            />
                                        ) : column.id === '__actions' ? null : (
                                            <div className="flex items-center gap-2 flex-1 min-w-0 overflow-hidden">
                                                {(column as ColumnConfig<T>).headerTooltip ? (
                                                    <Tooltip content={(column as ColumnConfig<T>).headerTooltip}>
                                                        {headerLabel}
                                                    </Tooltip>
                                                ) : headerLabel}
                                                {isSortable && (
                                                    <span className="shrink-0">
                                                        {isSorted ? (
                                                            effectiveSortDirection === 'asc' ?
                                                                <ArrowUp className="w-3 h-3 opacity-50" /> :
                                                                <ArrowDown className="w-3 h-3 opacity-50" />
                                                        ) : (
                                                            <ArrowUpDown className="w-3 h-3 opacity-50" />
                                                        )}
                                                    </span>
                                                )}
                                                {isReorderable && (
                                                    <span
                                                        draggable
                                                        title="Drag to reorder column"
                                                        className="ml-auto shrink-0 cursor-grab rounded p-0.5 text-white/25 opacity-0 transition-colors hover:bg-brand-main-700 hover:text-white/70 active:cursor-grabbing group-hover/header:opacity-100 light:text-black/25 light:hover:text-black/70"
                                                        onClick={(e) => e.stopPropagation()}
                                                        onMouseDown={(e) => e.stopPropagation()}
                                                        onDragStart={(e) => handleColumnDragStart(e, column.id)}
                                                        onDragEnd={handleColumnDragEnd}
                                                    >
                                                        <GripVertical className="size-3.5" />
                                                    </span>
                                                )}
                                            </div>
                                        )}

                                        {/* Resize handle */}
                                        {!isSpecialCol && enableResizing && (column as ColumnConfig<T>).resizable !== false && (
                                            <div
                                                className={cn(
                                                    'group absolute bottom-1 right-0 top-1 z-10 w-2 -mr-1 cursor-col-resize rounded-sm border-l border-transparent transition-colors',
                                                    'hover:border-brand-secondary-500/50 hover:bg-brand-secondary-500/10',
                                                    resizingColumn === column.id && 'border-brand-secondary-400/70 bg-brand-secondary-500/15',
                                                )}
                                                onMouseDown={(e) => handleResizeStart(e, column.id)}
                                                onDoubleClick={(e) => handleResizeReset(e, column.id)}
                                            >
                                                <div className={cn(
                                                    'absolute inset-y-0 right-0.5 w-px rounded-full bg-brand-main-600/80 transition-colors',
                                                    'group-hover:bg-brand-secondary-400',
                                                    resizingColumn === column.id && 'bg-brand-secondary-300',
                                                )} />
                                            </div>
                                        )}
                                    </div>
                                );
                            })}
                        </div>
                        {showSummaryRow && (
                            <div className="grid border-b border-brand-main-700/70 bg-brand-main-900/80 shadow-[0_10px_22px_-22px_rgba(0,0,0,0.9)] light:bg-white/95" style={gridStyle}>
                                {allColumns.map((column) => {
                                    const isSpecialCol = column.id === '__selection' || column.id === '__actions';
                                    const typedColumn = column as ColumnConfig<T>;
                                    const summary = !isSpecialCol ? columnSummaries.get(column.id) : null;

                                    return (
                                        <div
                                            key={`${column.id}:summary`}
                                            className={cn(
                                                'flex min-h-9 min-w-0 items-center overflow-hidden border-r border-brand-main-700/60 px-4 py-1.5 text-xs text-white/45 light:text-black/45',
                                                column.id === '__actions' && enableStickyActions && 'sticky right-0 z-20 border-l border-brand-main-600 bg-brand-main-900/95 shadow-[-10px_0_18px_-16px_rgba(0,0,0,0.8)] light:bg-white/95',
                                                !summary && 'text-white/20 light:text-black/20',
                                                forceLeftAlign ? alignClass('left') : alignClass(typedColumn.align),
                                            )}
                                        >
                                            {summary ? (
                                                <div className="min-w-0 truncate">{summary}</div>
                                            ) : null}
                                        </div>
                                    );
                                })}
                            </div>
                        )}
                    </div>

                    {/* Body */}
                    {/* Empty state */}
                    {stateMessage ? null : (
                        <>
                            {enableVirtualization ? (
                                <div
                                    style={{
                                        height: `${rowVirtualizer.getTotalSize()}px`,
                                        width: '100%',
                                        position: 'relative',
                                    }}
                                >
                                    {rowVirtualizer.getVirtualItems().map((virtualRow) => {
                                        const row = data[virtualRow.index];
                                        const key = rowKey ? rowKey(row) : virtualRow.index;
                                        const isSelected = selectedRows.has(key);
                                        const rowClass = typeof rowClassName === 'function' ? rowClassName(row) : rowClassName;

                                        return (
                                            <div
                                                key={key}
                                                ref={(el) => {
                                                    if (rowRefs && el) {
                                                        rowRefs.current.set(key, el as any);
                                                    }
                                                }}
                                                className={cn(
                                                    'group/row grid border-b border-white/5 py-0 transition-colors light:border-black/5',
                                                    onRowClick && 'cursor-pointer hover:bg-brand-main-500/30',
                                                    isSelected && 'bg-brand-secondary-500/10',
                                                    rowClass,
                                                )}
                                                style={{
                                                    gridTemplateColumns: gridTemplate,
                                                    width: shouldStretchToFill ? '100%' : `${calculatedTableWidth}px`,
                                                    position: 'absolute',
                                                    top: 0,
                                                    left: 0,
                                                    height: `${virtualRow.size}px`,
                                                    transform: `translateY(${virtualRow.start}px)`,
                                                }}
                                                onClick={(e) => {
                                                    const target = e.target as HTMLElement;
                                                    if (target.closest('button[data-slot="checkbox"]') || target.closest('[data-row-actions]')) return;
                                                    onRowClick?.(row);
                                                }}
                                            >
                                                {enableSelection && (
                                                    <div className="px-4 flex items-center text-white/90 light:text-black/90">
                                                        <Checkbox
                                                            checked={isSelected}
                                                            onCheckedChange={() => handleSelectRow(key)}
                                                            onClick={(e) => e.stopPropagation()}
                                                        />
                                                    </div>
                                                )}

                                                {renderRow ? renderRow(row, orderedColumns) : orderedColumns.map((column) => (
                                                    <DataCell
                                                        key={column.id}
                                                        row={row}
                                                        column={column}
                                                        density={density}
                                                        enableCellTooltips={enableCellTooltips}
                                                        enableOverflowTooltips={enableOverflowTooltips}
                                                        forceLeftAlign={forceLeftAlign}
                                                    />
                                                ))}

                                                {rowActions && rowActions.length > 0 && (
                                                    <div className={cn(
                                                        'flex items-center justify-center px-4 text-sm text-white/90 light:text-black/90',
                                                        density === 'compact' ? 'py-1.5' : 'py-2',
                                                        enableStickyActions && 'sticky right-0 z-10 border-l border-brand-main-700/70 bg-brand-main-950/95 shadow-[-10px_0_18px_-16px_rgba(0,0,0,0.8)] light:bg-white/95',
                                                    )}>
                                                        <RowActionsMenu row={row} actions={rowActions} />
                                                    </div>
                                                )}
                                            </div>
                                        );
                                    })}
                                </div>
                            ) : (
                                data.map((row, rowIdx) => {
                                    const key = rowKey ? rowKey(row) : rowIdx;
                                    const isSelected = selectedRows.has(key);
                                    const rowClass = typeof rowClassName === 'function' ? rowClassName(row) : rowClassName;

                                    return (
                                        <div
                                            key={key}
                                            ref={(el) => {
                                                if (rowRefs && el) {
                                                    rowRefs.current.set(key, el as any);
                                                }
                                            }}
                                            className={cn(
                                                'group/row grid border-b border-white/5 py-0 transition-colors light:border-black/5',
                                                onRowClick && 'cursor-pointer hover:bg-brand-main-500/30',
                                                isSelected && 'bg-brand-secondary-500/10',
                                                rowClass,
                                            )}
                                            style={{
                                                gridTemplateColumns: gridTemplate,
                                                width: shouldStretchToFill ? '100%' : `${calculatedTableWidth}px`,
                                            }}
                                            onClick={(e) => {
                                                const target = e.target as HTMLElement;
                                                if (target.closest('button[data-slot="checkbox"]') || target.closest('[data-row-actions]')) return;
                                                onRowClick?.(row);
                                            }}
                                        >
                                            {enableSelection && (
                                                <div className="px-4 flex items-center text-white/90 light:text-black/90">
                                                    <Checkbox
                                                        checked={isSelected}
                                                        onCheckedChange={() => handleSelectRow(key)}
                                                        onClick={(e) => e.stopPropagation()}
                                                    />
                                                </div>
                                            )}

                                            {renderRow ? renderRow(row, orderedColumns) : orderedColumns.map((column) => (
                                                <DataCell
                                                    key={column.id}
                                                    row={row}
                                                    column={column}
                                                    density={density}
                                                    enableCellTooltips={enableCellTooltips}
                                                    enableOverflowTooltips={enableOverflowTooltips}
                                                    forceLeftAlign={forceLeftAlign}
                                                />
                                            ))}

                                            {rowActions && rowActions.length > 0 && (
                                                <div className={cn(
                                                    'flex items-center justify-center px-4 text-sm text-white/90 light:text-black/90',
                                                    density === 'compact' ? 'py-1.5' : 'py-2',
                                                    enableStickyActions && 'sticky right-0 z-10 border-l border-brand-main-700/70 bg-brand-main-950/95 shadow-[-10px_0_18px_-16px_rgba(0,0,0,0.8)] light:bg-white/95',
                                                )}>
                                                    <RowActionsMenu row={row} actions={rowActions} />
                                                </div>
                                            )}
                                        </div>
                                    );
                                })
                            )}

                            {/* Loading indicator for infinite scroll */}
                            {isLoadingMore && data.length > 0 && (
                                <div className="w-full py-4 flex items-center justify-center border-t border-white/5 light:border-black/5">
                                    <div className="flex items-center gap-2 text-white/70 text-sm light:text-black/70">
                                        <div className="w-4 h-4 border-2 border-white/30 border-t-white/70 rounded-full animate-spin light:border-black/30" />
                                        <span>Loading more...</span>
                                    </div>
                                </div>
                            )}
                        </>
                    )}
                </div>
            </div>
            {stateMessage ? (
                <div
                    className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center px-4 text-white/70 light:text-black/70"
                    aria-live="polite"
                >
                    <div className="pointer-events-auto max-w-full">
                        {stateMessage}
                    </div>
                </div>
            ) : null}
            </div>
        </div>
        </TooltipProvider>
    );
}

// Row actions dropdown menu component
function RowActionsMenu<T>({ row, actions }: { row: T; actions: RowAction<T>[] }) {
    const [open, setOpen] = useState(false);
    const visibleActions = actions.filter((action) => !action.hidden?.(row));

    if (visibleActions.length === 0) return null;

    return (
        <div className="relative" data-row-actions>
            <Popover open={open} onOpenChange={setOpen}>
                <PopoverTrigger asChild>
                    <button
                        className="p-0.5 rounded hover:bg-brand-main-500/50 transition-colors outline-none"
                        onClick={(e) => e.stopPropagation()}
                    >
                        <MoreVertical className="w-4 h-4 text-white/70 light:text-black/70" />
                    </button>
                </PopoverTrigger>
                <PopoverContent className="w-56 p-1 bg-brand-main-800 border border-brand-main-600 shadow-lg z-50" align="end">
                    {visibleActions.map((action, idx) => {
                        const isDisabled = action.disabled?.(row) || false;
                        const disabledReason = isDisabled ? action.disabledReason?.(row) : undefined;
                        const previousGroup = idx > 0 ? visibleActions[idx - 1]?.group : undefined;
                        const showGroupDivider = idx > 0 && action.group !== previousGroup;

                        const item = (
                            <button
                                onClick={(e) => {
                                    e.stopPropagation();
                                    if (!isDisabled) {
                                        action.onClick(row);
                                        setOpen(false);
                                    }
                                }}
                                disabled={isDisabled}
                                className={`w-full px-3 py-2 text-left text-sm flex items-center gap-2 transition-colors rounded ${isDisabled
                                    ?'cursor-not-allowed opacity-45'
                                    : action.variant === 'destructive'
                                        ? 'text-red-400 hover:bg-red-500/20'
                                        : 'text-white/90 hover:bg-brand-main-700 light:text-black/90'
                                    }`}
                            >
                                {action.icon && <span className="shrink-0">{action.icon}</span>}
                                <span className="min-w-0 flex-1 truncate">{action.label}</span>
                                {action.shortcut && (
                                    <span className="shrink-0 rounded border border-brand-main-600 px-1.5 py-0.5 font-mono text-[10px] text-white/35 light:text-black/35">
                                        {action.shortcut}
                                    </span>
                                )}
                            </button>
                        );

                        return (
                            <div key={`${action.group || 'default'}-${action.label}-${idx}`}>
                                {showGroupDivider && <div className="my-1 h-px bg-brand-main-600" />}
                                {disabledReason ? (
                                    <Tooltip content={disabledReason}>
                                        <span className="block">{item}</span>
                                    </Tooltip>
                                ) : item}
                            </div>
                        );
                    })}
                </PopoverContent>
            </Popover>
        </div>
    );
}
