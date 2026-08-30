export enum ChatType {
    CHAT = 'ChatCompletion',
    CHAT_STREAMING = 'ChatStreaming',
    EMBEDDINGS = 'Embeddings',
}

export const ChatTypeToLabel = {
    [ChatType.CHAT]: 'CHAT',
    [ChatType.CHAT_STREAMING]: 'CHAT STREAM',
    [ChatType.EMBEDDINGS]: 'EMBEDDINGS',
}

export const ChatTypeToDescription = {
    [ChatType.CHAT]: 'Chat completion is the process of generating a response to a user\'s message.',
    [ChatType.CHAT_STREAMING]: 'Chat streaming is the process of generating a response to a user\'s message in real-time.',
    [ChatType.EMBEDDINGS]: 'Embeddings are a way to represent text as a vector of numbers.',
}

export const getChatTypeLabel = (chatType: ChatType) => {
    return {
        label: ChatTypeToLabel[chatType],
        description: ChatTypeToDescription[chatType],
    }
}