interface ErrorInfo {
  message?: string;
  estack?: string;
  diff?: string | null;
}

interface InlineErrorDisplayProps {
  errorsJson: string;
}

/** Compact inline error display for individual attempt errors */
export function InlineErrorDisplay({ errorsJson }: InlineErrorDisplayProps) {
  let errorText = '';

  try {
    const parsed = JSON.parse(errorsJson);
    if (Array.isArray(parsed) && parsed.length > 0) {
      if (typeof parsed[0] === 'string') {
        // Jest-stare/Detox/Playwright JUnit format: array of full error strings
        errorText = parsed.join('\n\n');
      } else {
        // Playwright format: array of error objects
        errorText = parsed
          .map((e: ErrorInfo) => {
            const parts = [e.message];
            if (e.estack) parts.push(e.estack);
            return parts.join('\n');
          })
          .join('\n\n');
      }
    } else if (parsed && typeof parsed === 'object' && parsed.message) {
      // Cypress format: single error object
      const parts = [parsed.message];
      if (parsed.estack) parts.push(parsed.estack);
      errorText = parts.join('\n');
    }
  } catch {
    // Invalid JSON
  }

  if (!errorText) return null;

  return (
    <div className="ml-5 rounded border border-red-200 bg-gray-900 dark:border-red-800 overflow-hidden">
      <pre className="p-3 overflow-x-auto text-xs font-mono text-gray-100 whitespace-pre-wrap">
        {errorText}
      </pre>
    </div>
  );
}
