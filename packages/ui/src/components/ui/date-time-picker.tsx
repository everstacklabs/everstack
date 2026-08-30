"use client"

import { useState, useMemo, useEffect } from "react"
import { Calendar } from "./calendar"
import { Input } from "./input"
import { Label } from "./label"
import { Button } from "./button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./select"
import type { DateRange } from "react-day-picker"
import { z } from "zod"

// Validation schemas
const timeObjectSchema = z.object({
    hour: z.number().min(0).max(12),
    minute: z.number().min(0).max(59),
    second: z.number().min(0).max(59),
    period: z.enum(['AM', 'PM'])
})

// Function to validate that a date + time combination is not in the future
const validateDateTimeNotInFuture = (date: Date, timeObj: { hour: number; minute: number; second: number; period: 'AM' | 'PM' }): boolean => {
    // Convert 12-hour time to 24-hour
    let hour24 = timeObj.hour
    if (timeObj.period === 'PM' && hour24 !== 12) {
        hour24 += 12
    } else if (timeObj.period === 'AM' && hour24 === 12) {
        hour24 = 0
    }

    // Create the full datetime
    const fullDateTime = new Date(date)
    fullDateTime.setHours(hour24, timeObj.minute, timeObj.second, 0)

    // Check if it's in the past
    return fullDateTime <= new Date()
}

export function DateTimePicker({
    dateRange,
    onDateRangeChange,
    onOpenChange,
}: {
    dateRange: { start: Date | undefined; end: Date | undefined } | null
    onDateRangeChange: (range: { start: Date; end: Date }) => void
    onOpenChange: (open: boolean) => void
}) {
    // Compute initial values from props using useMemo
    const initialSelectedRange = useMemo<DateRange | undefined>(() =>
        dateRange?.start && dateRange?.end
            ? { from: dateRange.start, to: dateRange.end }
            : undefined,
        [dateRange?.start, dateRange?.end]
    )

    // Convert 24-hour to 12-hour format with AM/PM
    const convertTo12Hour = useMemo(() => (date: Date | undefined) => {
        if (!date) return { hour: 12, minute: 0, second: 0, period: 'AM' as const }
        const hour24 = date.getHours()
        const hour12 = hour24 % 12 || 12
        const period = hour24 >= 12 ? 'PM' : 'AM'
        return {
            hour: hour12,
            minute: date.getMinutes(),
            second: date.getSeconds(),
            period: period as 'AM' | 'PM'
        }
    }, [])

    const initialStartTime = useMemo(() => convertTo12Hour(dateRange?.start), [dateRange?.start, convertTo12Hour])
    const initialEndTime = useMemo(() => convertTo12Hour(dateRange?.end), [dateRange?.end, convertTo12Hour])

    // Interactive state for user selections (initialized with computed values)
    const [selectedRange, setSelectedRange] = useState<DateRange | undefined>(initialSelectedRange)
    const [startTime, setStartTime] = useState(initialStartTime)
    const [endTime, setEndTime] = useState(initialEndTime)

    // Validation errors state
    const [validationErrors, setValidationErrors] = useState<{
        startTime?: string
        endTime?: string
        dateRange?: string
    }>({})

    // Sync state when initial values change (when props change)
    useEffect(() => {
        setSelectedRange(initialSelectedRange)
        setStartTime(initialStartTime)
        setEndTime(initialEndTime)
    }, [initialSelectedRange, initialStartTime, initialEndTime])

    // Clear validation errors when selectedRange changes
    useEffect(() => {
        setValidationErrors({})
    }, [selectedRange])

    // Convert 12-hour format back to 24-hour
    const convertTo24Hour = useMemo(() => (timeObj: typeof startTime) => {
        let hour24 = timeObj.hour
        if (timeObj.period === 'PM' && hour24 !== 12) {
            hour24 += 12
        } else if (timeObj.period === 'AM' && hour24 === 12) {
            hour24 = 0
        }
        return { hour: hour24, minute: timeObj.minute, second: timeObj.second }
    }, [])

    // Validation functions
    const validateTimeObject = (timeObj: typeof startTime): string | null => {
        try {
            timeObjectSchema.parse(timeObj)
            return null
        } catch {
            return "Invalid time format"
        }
    }

    const validateDateTimeNotFuture = (date: Date | undefined, timeObj: typeof startTime, field: 'start' | 'end'): string | null => {
        if (!date) return null
        const isValid = validateDateTimeNotInFuture(date, timeObj)
        return isValid ? null : `${field === 'start' ? 'Start' : 'End'} date and time cannot be in the future`
    }

    // Helpers for numeric 2-digit input handling similar to common date pickers
    const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max)

    const handleNumericKeyDown = (
        e: React.KeyboardEvent<HTMLInputElement>,
        current: number,
        min: number,
        max: number,
        defaultWhenEmpty: number,
        apply: (next: number) => void,
    ) => {
        const key = e.key

        // Allow navigation keys
        const allowed = [
            'Tab', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'Shift', 'Meta', 'Control'
        ]
        if (allowed.includes(key)) return

        // Backspace behavior -> set to default (00 or 01)
        if (key === 'Backspace') {
            e.preventDefault()
            apply(defaultWhenEmpty)
            return
        }

        // Only accept digits
        if (!/^\d$/.test(key)) {
            e.preventDefault()
            return
        }

        e.preventDefault()
        const digit = parseInt(key, 10)
        const input = e.target as HTMLInputElement
        const selStart = input.selectionStart ?? 0
        const selEnd = input.selectionEnd ?? 0
        const currentValue = input.value
        const selectionLength = selEnd - selStart

        let next: number
        // Check if entire value is selected (replacing)
        if (selectionLength === currentValue.length) {
            // Replacing full value with a single digit
            next = digit
        } else if (selectionLength > 0) {
            // Has partial selection, replace selection with digit
            next = digit
        } else {
            // No selection - check if we should append or replace
            const currentStr = currentValue.replace(/^0+/, '') || '0' // remove leading zeros, but keep '0' if empty

            // Only append if:
            // 1. We have exactly 1 significant digit (e.g., "01" -> "1", "03" -> "3")
            // 2. The result of appending would be valid (within min-max range)
            if (currentStr.length === 1) {
                const appended = parseInt(currentStr + digit, 10)
                // Check if appending would create a valid value
                if (appended >= min && appended <= max) {
                    // Append: "1" + "2" = "12"
                    next = appended
                } else {
                    // Would exceed range, replace instead: "5" + "9" in hour field = "09" not "59"
                    next = digit
                }
            } else {
                // Already 2 digits or 0, replace with new single digit
                next = digit
            }
        }

        next = clamp(next, min, max)
        apply(next)
    }

    // Helper to create time input props
    const createTimeInputProps = (
        field: 'hour' | 'minute' | 'second',
        timeType: 'start' | 'end',
        value: number,
        min: number,
        max: number,
        defaultValue: number
    ) => {
        const time = timeType === 'start' ? startTime : endTime
        const setTime = timeType === 'start' ? setStartTime : setEndTime
        const dateField = timeType === 'start' ? selectedRange?.from : selectedRange?.to
        const errorKey = timeType === 'start' ? 'startTime' : 'endTime'

        return {
            type: "text" as const,
            inputMode: "numeric" as const,
            placeholder: field === 'hour' ? 'HH' : field === 'minute' ? 'MM' : 'SS',
            maxLength: 2,
            value: value.toString().padStart(2, '0'),
            onKeyDown: (e: React.KeyboardEvent<HTMLInputElement>) =>
                handleNumericKeyDown(
                    e,
                    value,
                    min,
                    max,
                    defaultValue,
                    (next) => {
                        const newTime = { ...time, [field]: next }
                        setTime(newTime)
                        const timeError = validateTimeObject(newTime)
                        const futureError = dateField ? validateDateTimeNotFuture(dateField, newTime, timeType) : null
                        setValidationErrors(prev => ({ ...prev, [errorKey]: timeError || futureError || undefined }))
                    }
                ),
            onChange: (e: React.ChangeEvent<HTMLInputElement>) => {
                const raw = e.target.value
                if (raw.length > 2) return
                if (raw === '') {
                    const newTime = { ...time, [field]: defaultValue }
                    setTime(newTime)
                    const timeError = validateTimeObject(newTime)
                    const futureError = dateField ? validateDateTimeNotFuture(dateField, newTime, timeType) : null
                    setValidationErrors(prev => ({ ...prev, [errorKey]: timeError || futureError || undefined }))
                    return
                }
                const parsed = parseInt(raw, 10)
                if (Number.isNaN(parsed)) return
                const clamped = Math.min(Math.max(parsed, min), max)
                const newTime = { ...time, [field]: clamped }
                setTime(newTime)
                const timeError = validateTimeObject(newTime)
                const futureError = dateField ? validateDateTimeNotFuture(dateField, newTime, timeType) : null
                setValidationErrors(prev => ({ ...prev, [errorKey]: timeError || futureError || undefined }))
            },
            onFocus: (e: React.FocusEvent<HTMLInputElement>) => {
                e.stopPropagation();
                // Delay select to allow input to fully focus first
                requestAnimationFrame(() => {
                    e.target.select()
                })
            },
            onClick: (e: React.MouseEvent<HTMLInputElement>) => e.stopPropagation(),
            className: `text-center ${validationErrors[errorKey] ? 'border-red-500' : ''} w-auto max-w-12`
        }
    }

    // Helper to create period select (AM/PM)
    const CreatePeriodSelect = ({ timeType }: { timeType: 'start' | 'end' }) => {
        const time = timeType === 'start' ? startTime : endTime
        const setTime = timeType === 'start' ? setStartTime : setEndTime
        const dateField = timeType === 'start' ? selectedRange?.from : selectedRange?.to
        const errorKey = timeType === 'start' ? 'startTime' : 'endTime'

        return (
            <Select
                value={time.period}
                onValueChange={(value: 'AM' | 'PM') => {
                    const newTime = { ...time, period: value }
                    setTime(newTime)
                    // Validate future date
                    const futureError = dateField ? validateDateTimeNotFuture(dateField, newTime, timeType) : null
                    setValidationErrors(prev => ({
                        ...prev,
                        [errorKey]: futureError || undefined
                    }))
                }}
            >
                <SelectTrigger className="w-auto border-brand-main-500">
                    <SelectValue />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="AM">AM</SelectItem>
                    <SelectItem value="PM">PM</SelectItem>
                </SelectContent>
            </Select>
        )
    }

    const handleApply = () => {
        if (!selectedRange?.from || !selectedRange?.to) return

        // Check for validation errors
        const startTimeError = validateDateTimeNotFuture(selectedRange.from, startTime, 'start')
        const endTimeError = validateDateTimeNotFuture(selectedRange.to, endTime, 'end')

        if (startTimeError || endTimeError) {
            setValidationErrors({
                startTime: startTimeError || undefined,
                endTime: endTimeError || undefined,
                dateRange: undefined
            })
            return
        }

        // Convert 12-hour times to 24-hour and merge with dates
        const start24 = convertTo24Hour(startTime)
        const end24 = convertTo24Hour(endTime)

        const startDate = new Date(selectedRange.from)
        startDate.setHours(start24.hour, start24.minute, start24.second, 0)

        const endDate = new Date(selectedRange.to)
        endDate.setHours(end24.hour, end24.minute, end24.second, 0)

        onDateRangeChange({ start: startDate, end: endDate })
        onOpenChange(false)
    }

    return (
        <div
            className="flex flex-col gap-4"
            onClick={(e) => e.stopPropagation()}
            onMouseDown={(e) => e.stopPropagation()}
        >
            <div className="px-4">
                <Calendar
                    mode="range"
                    selected={selectedRange}
                    captionLayout="label"
                    onSelect={setSelectedRange}
                    numberOfMonths={1}
                    showOutsideDays={true}
                    fixedWeeks
                    disabled={(date) => date > new Date()}
                />
            </div>
            <div className="border-t border-brand-main-600 px-4 pt-4">
                <div className="flex flex-col gap-3">
                    {/* Start Time */}
                    <div className="flex flex-col gap-2">
                        <Label className="text-xs font-semibold">Start Time</Label>
                        <div className="flex items-center gap-2">
                            <Input {...createTimeInputProps('hour', 'start', startTime.hour, 0, 12, 0)} />
                            <span className="text-sm">:</span>
                            <Input {...createTimeInputProps('minute', 'start', startTime.minute, 0, 59, 0)} />
                            <span className="text-sm">:</span>
                            <Input {...createTimeInputProps('second', 'start', startTime.second, 0, 59, 0)} />
                            <CreatePeriodSelect timeType="start" />
                        </div>
                        {validationErrors.startTime && (
                            <p className="text-xs text-red-500 mt-1">{validationErrors.startTime}</p>
                        )}
                    </div>

                    {/* End Time */}
                    <div className="flex flex-col gap-2">
                        <Label className="text-xs font-semibold">End Time</Label>
                        <div className="flex items-center gap-2">
                            <Input {...createTimeInputProps('hour', 'end', endTime.hour, 0, 12, 1)} />
                            <span className="text-sm">:</span>
                            <Input {...createTimeInputProps('minute', 'end', endTime.minute, 0, 59, 0)} />
                            <span className="text-sm">:</span>
                            <Input {...createTimeInputProps('second', 'end', endTime.second, 0, 59, 0)} />
                            <CreatePeriodSelect timeType="end" />
                        </div>
                        {validationErrors.endTime && (
                            <p className="text-xs text-red-500 mt-1">{validationErrors.endTime}</p>
                        )}
                    </div>
                </div>
            </div>
            <div className="px-4 pb-4">
                <Button
                    onClick={handleApply}
                    disabled={!selectedRange?.from || !selectedRange?.to}
                    className="w-full"
                >
                    Apply Range
                </Button>
            </div>
        </div>
    )
}
