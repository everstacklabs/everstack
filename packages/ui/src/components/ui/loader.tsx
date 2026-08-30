import React from 'react'
import { Spinner } from './spinner'

const Loader = ({ loaderText }: { loaderText: string }) => {
    return (
        <div className="flex items-center justify-center h-full">
            <div className="flex items-center text-center space-x-2">
                <Spinner />
                <p className="text-sm text-gray-300 light:text-gray-700">{loaderText}</p>
            </div>
        </div>
    )
}

export { Loader }