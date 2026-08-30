"use client";

import type { BundledLanguage } from './code-block.js';
import {
    CodeBlock,
    CodeBlockBody,
    CodeBlockContent,
    CodeBlockCopyButton,
    CodeBlockItem,
} from './code-block.js';


const codeBlockData = (code: Record<string, unknown>) => [
    {
        language: 'json',
        filename: 'data.json',
        code: JSON.stringify(code, null, 2),
    }
]

export const JsonBlock = ({ code }: { code: Record<string, unknown> }) => (
    <CodeBlock className='scrollbar-macos overflow-y-auto h-full' data={codeBlockData(code)} defaultValue={(codeBlockData(code)[0]?.language ?? 'json') as BundledLanguage}>
        <CodeBlockBody>
            {(item) => (
                <CodeBlockItem key={item.language} value={item.language} className='relative'>
                    <CodeBlockContent language={item.language as BundledLanguage}>
                        {item.code}
                    </CodeBlockContent>
                    <div className='absolute right-2 top-2 flex justify-end'>
                        <CodeBlockCopyButton
                            size='sm'
                            onCopy={() => console.log('Copied code to clipboard')}
                            onError={() => console.error('Failed to copy code to clipboard')}
                        />
                    </div>
                </CodeBlockItem>
            )}
        </CodeBlockBody>
    </CodeBlock>
);