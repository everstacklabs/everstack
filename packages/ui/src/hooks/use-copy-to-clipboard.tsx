import { useCallback, useState } from "react";

export type UseCopyToClipboardReturn = [
  (text: string) => void,
  boolean | Error
];

export const useCopyToClipboard = (): UseCopyToClipboardReturn => {
  const [clipboardState, setClipboardState] = useState<boolean | Error>(false);

  const copy = useCallback((text: string): void => {
    // Check if clipboard API is available
    if (!navigator?.clipboard) {
      // Fallback for insecure contexts
      try {
        const textArea = document.createElement('textarea');
        textArea.value = text;
        textArea.style.position = 'fixed';
        textArea.style.left = '-999999px';
        textArea.style.top = '-999999px';
        document.body.appendChild(textArea);
        textArea.focus();
        textArea.select();

        const successful = document.execCommand('copy');
        document.body.removeChild(textArea);

        if (successful) {
          setClipboardState(true);
          setTimeout(() => setClipboardState(false), 2000);
        } else {
          const error = new Error('Copy command failed');
          setClipboardState(error);
          setTimeout(() => setClipboardState(false), 2000);
        }
      } catch (error) {
        setClipboardState(error instanceof Error ? error : new Error('Copy failed'));
        setTimeout(() => setClipboardState(false), 2000);
      }
      return;
    }

    // Use modern clipboard API
    navigator.clipboard
      .writeText(text)
      .then(() => {
        setClipboardState(true);
        setTimeout(() => setClipboardState(false), 2000);
      })
      .catch((error) => {
        setClipboardState(error);
        setTimeout(() => setClipboardState(false), 2000);
      });
  }, []);

  return [copy, clipboardState];
};