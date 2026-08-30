import { type Event } from '@everstack/proto/everstack/events/v1/events_service_pb'
import { Iconify, Loader2, Maximize2, ui } from '@everstack/ui';
import { JsonBlock } from '@everstack/ui/components';
import { useState } from 'react';
import { transformEventForUser, getEventTypeLabel, isEventVisibleToUser } from '@/lib/event-transform';
import { ResponsiveTable, type ColumnConfig } from '@/ui/table';
import { AlertCircle } from 'lucide-react';

const { Dialog, DialogContent, DialogTitle } = ui;

export const EventsTable = ({
    pageEvents,
    isLoading,
    error,
}: {
    pageEvents: Event[],
    isLoading?: boolean,
    error?: Error | null,
}) => {
    const [isExpanded, setIsExpanded] = useState(false);
    const [currentRowId, setCurrentRowId] = useState<string | null>(null);

    // Filter and transform events for user display
    const userEvents = pageEvents
        .filter(event => isEventVisibleToUser(event.type))
        .map(event => ({
            original: event,
            transformed: transformEventForUser(event)
        }));

    const handleDialogClose = () => {
        setIsExpanded(false);
    };

    const handleMouseLeave = () => {
        if (currentRowId && !isExpanded) {
            setCurrentRowId(null);
        }
    };

    const columns: ColumnConfig[] = [
        {
            id: 'type',
            header: 'Type',
            width: 200,
            minWidth: 150,
            render: (row: typeof userEvents[0]) => (
                <span className='break-words'>{row.original.type}</span>
            )
        },
        {
            id: 'details',
            header: 'Details',
            width: 400,
            minWidth: 250,
            resizable: true,
            render: (row: typeof userEvents[0]) => (
                <div
                    className='flex justify-between items-center w-full min-w-0'
                    onMouseEnter={() => setCurrentRowId(row.original.id)}
                    onMouseLeave={handleMouseLeave}
                >
                    <pre className='whitespace-nowrap py-0.5 flex-1 overflow-hidden overflow-ellipsis font-mono text-xs min-w-0'>
                        {JSON.stringify(row.transformed.displayData)}
                    </pre>
                    {currentRowId === row.original.id && (
                        <button
                            type='button'
                            aria-label='Expand payload'
                            className='rounded p-0.5 hover:bg-white hover:text-brand-main-950 cursor-pointer flex-shrink-0 ml-2'
                            onClick={(e) => {
                                e.stopPropagation();
                                setCurrentRowId(row.original.id);
                                setIsExpanded(true);
                            }}
                        >
                            <Maximize2 size={14} />
                        </button>
                    )}
                </div>
            )
        },
        {
            id: 'createdAt',
            header: 'Created At',
            width: 180,
            minWidth: 140,
            render: (row: typeof userEvents[0]) => (
                <span className='break-words text-sm text-white/70 light:text-black/70'>{row.transformed.createdAt}</span>
            )
        },
        {
            id: 'payloadSize',
            header: 'Payload Size',
            width: 120,
            minWidth: 100,
            render: (row: typeof userEvents[0]) => row.original.payloadSizeBytes
        }
    ];

    return (
        <>
            <ResponsiveTable
                columns={columns}
                data={userEvents}
                isLoading={isLoading}
                minTableWidth='750px'
                emptyMessage={
                    <div className='flex text-sm items-center justify-center'>
                        {error ? (
                            <div className='flex flex-col items-center justify-center space-y-3 text-center px-4'>
                                <AlertCircle className='size-12 text-rose-400' />
                                <div className='space-y-1'>
                                    <div className='text-white/90 font-semibold light:text-black/90'>Error Loading Events</div>
                                    <div className='text-white/50 text-xs max-w-md light:text-black/50'>
                                        {error.message || 'An unexpected error occurred while loading events.'}
                                    </div>
                                </div>
                            </div>
                        ) : isLoading ? (
                            <div className='flex items-center justify-center space-x-2'>
                                <Loader2 className='size-4 animate-spin text-brand-main-100' />
                                <span className='text-brand-main-100 font-normal'>Loading events...</span>
                            </div>
                        ) : (
                            <div className='flex flex-col items-center justify-center'>
                                <div className='relative mb-6'>
                                    <div className='absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl' />
                                    <div className='relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4'>
                                        <Iconify.Icon icon='solar:list-outline' className='size-8 text-brand-secondary-400' />
                                    </div>
                                </div>
                                <h3 className='text-base font-medium text-white mb-2 light:text-brand-main-50'>No events found</h3>
                                <p className='text-sm text-white/50 max-w-sm text-center leading-relaxed light:text-black/50'>
                                    No events found for the selected time range. Try adjusting your time range.
                                </p>
                            </div>
                        )}
                    </div>
                }
                renderRow={(row, columns) =>
                    columns.map((column, colIdx) => (
                        <div
                            key={colIdx}
                            className={`px-4 py-1 flex items-center text-sm text-white/90 min-w-0 light:text-black/90${column.className || ''} light:text-black/90`}
                        >
                            {column.render ? column.render(row) : null}
                        </div>
                    ))
                }
            />
            <Dialog open={isExpanded} onOpenChange={handleDialogClose}>
                <DialogContent className='w-[700px] max-h-[80vh] overflow-y-auto scrollbar-macos'>
                    <DialogTitle>
                        <div className='text-lg font-semibold flex justify-between items-center gap-1'>
                            {(() => {
                                const userEvent = userEvents.find(({ original }) => original.id === currentRowId)
                                return userEvent ? getEventTypeLabel(userEvent.original.type) : 'Event Details'
                            })()}
                        </div>
                        <div className='text-xs text-white/70 light:text-black/70'>
                            {(() => {
                                const userEvent = userEvents.find(({ original }) => original.id === currentRowId)
                                return userEvent?.transformed.createdAt
                            })()}
                        </div>
                        {(() => {
                            const userEvent = userEvents.find(({ original }) => original.id === currentRowId)
                            return userEvent?.transformed.message && (
                                <div className='text-sm text-white/80 mt-2 light:text-black/80'>
                                    {userEvent.transformed.message}
                                </div>
                            )
                        })()}
                    </DialogTitle>
                    <div className='overflow-y-auto scrollbar-macos h-full'>
                        <JsonBlock
                            code={
                                userEvents.find(({ original }) => original.id === currentRowId)?.transformed.displayData || {}
                            }
                        />
                    </div>
                </DialogContent>
            </Dialog>
        </>
    )
}
