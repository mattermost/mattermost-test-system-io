import * as React from 'react';
import {
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  X,
  ZoomIn,
  Download,
  Image as ImageIcon,
} from 'lucide-react';
import { Dialog, DialogPortal, DialogOverlay } from './dialog';
import * as DialogPrimitive from '@radix-ui/react-dialog';
import { cn } from '@/lib/utils';
import type { TestAttachment } from '@/types';

interface ScreenshotGalleryProps {
  screenshots: TestAttachment[];
  className?: string;
}

/** Get the image URL from attachment */
function getImageUrl(attachment: TestAttachment): string {
  return `/files/${attachment.s3_key}`;
}

/** Get filename from path */
function getFilename(attachment: TestAttachment): string {
  return attachment.path.split('/').pop() ?? 'screenshot.png';
}

export const ScreenshotGallery = React.memo(function ScreenshotGallery({
  screenshots,
  className,
}: ScreenshotGalleryProps) {
  const [isOpen, setIsOpen] = React.useState(false);
  const [currentIndex, setCurrentIndex] = React.useState(0);

  const openLightbox = React.useCallback((index: number) => {
    setCurrentIndex(index);
    setIsOpen(true);
  }, []);

  const closeLightbox = React.useCallback(() => {
    setIsOpen(false);
  }, []);

  const goToPrevious = React.useCallback(() => {
    setCurrentIndex((prev) => (prev === 0 ? screenshots.length - 1 : prev - 1));
  }, [screenshots.length]);

  const goToNext = React.useCallback(() => {
    setCurrentIndex((prev) => (prev === screenshots.length - 1 ? 0 : prev + 1));
  }, [screenshots.length]);

  // Keyboard navigation
  React.useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'ArrowLeft') {
        e.preventDefault();
        goToPrevious();
      } else if (e.key === 'ArrowRight') {
        e.preventDefault();
        goToNext();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, goToPrevious, goToNext]);

  const currentScreenshot = screenshots[currentIndex];

  return (
    <>
      {/* Thumbnail Grid */}
      <div className={cn('flex flex-wrap gap-2', className)}>
        {screenshots.map((screenshot, idx) => (
          <button
            key={idx}
            type="button"
            onClick={() => openLightbox(idx)}
            className="group relative cursor-pointer focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 rounded"
          >
            <img
              src={getImageUrl(screenshot)}
              alt={`Screenshot ${idx + 1}`}
              className="h-24 w-auto rounded border border-gray-200 object-cover transition-all hover:border-blue-500 hover:shadow-md dark:border-gray-700 dark:hover:border-blue-400"
              loading="lazy"
            />
            <div className="absolute inset-0 flex items-center justify-center bg-black/0 transition-colors group-hover:bg-black/20 rounded">
              <ZoomIn className="h-6 w-6 text-white opacity-0 transition-opacity group-hover:opacity-100 drop-shadow-lg" />
            </div>
          </button>
        ))}
      </div>

      {/* Lightbox Dialog */}
      <Dialog open={isOpen} onOpenChange={closeLightbox}>
        <DialogPortal>
          <DialogOverlay className="bg-black/90" />
          <DialogPrimitive.Content
            className="fixed inset-0 z-50 flex items-center justify-center focus:outline-none"
            onPointerDownOutside={closeLightbox}
          >
            {/* Close button */}
            <button
              type="button"
              onClick={closeLightbox}
              className="absolute right-4 top-4 z-50 rounded-full bg-black/50 p-2 text-white transition-colors hover:bg-black/70 focus:outline-none focus:ring-2 focus:ring-white"
              aria-label="Close"
            >
              <X className="h-6 w-6" />
            </button>

            {/* Navigation: Previous */}
            {screenshots.length > 1 && (
              <button
                type="button"
                onClick={goToPrevious}
                className="absolute left-4 top-1/2 z-50 -translate-y-1/2 rounded-full bg-black/50 p-2 text-white transition-colors hover:bg-black/70 focus:outline-none focus:ring-2 focus:ring-white"
                aria-label="Previous screenshot"
              >
                <ChevronLeft className="h-8 w-8" />
              </button>
            )}

            {/* Navigation: Next */}
            {screenshots.length > 1 && (
              <button
                type="button"
                onClick={goToNext}
                className="absolute right-4 top-1/2 z-50 -translate-y-1/2 rounded-full bg-black/50 p-2 text-white transition-colors hover:bg-black/70 focus:outline-none focus:ring-2 focus:ring-white"
                aria-label="Next screenshot"
              >
                <ChevronRight className="h-8 w-8" />
              </button>
            )}

            {/* Main Image */}
            <div className="flex max-h-[85vh] max-w-[90vw] flex-col items-center">
              {currentScreenshot && (
                <img
                  src={getImageUrl(currentScreenshot)}
                  alt={`Screenshot ${currentIndex + 1}`}
                  className="max-h-[75vh] max-w-full rounded-lg object-contain shadow-2xl"
                />
              )}

              {/* Bottom bar with info and actions */}
              <div className="mt-4 flex items-center gap-4 rounded-lg bg-black/60 px-4 py-2 text-white">
                <div className="flex items-center gap-2">
                  <ImageIcon className="h-4 w-4" />
                  <span className="text-sm font-medium">
                    {currentIndex + 1} / {screenshots.length}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  {currentScreenshot && (
                    <>
                      <a
                        href={getImageUrl(currentScreenshot)}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="rounded p-1.5 transition-colors hover:bg-white/20 focus:outline-none focus:ring-2 focus:ring-white"
                        title="Open in new tab"
                      >
                        <ExternalLink className="h-4 w-4" />
                      </a>
                      <a
                        href={getImageUrl(currentScreenshot)}
                        download={getFilename(currentScreenshot)}
                        className="rounded p-1.5 transition-colors hover:bg-white/20 focus:outline-none focus:ring-2 focus:ring-white"
                        title="Download"
                      >
                        <Download className="h-4 w-4" />
                      </a>
                    </>
                  )}
                </div>
              </div>
            </div>

            {/* Keyboard hint */}
            <div className="absolute bottom-4 left-1/2 -translate-x-1/2 text-xs text-gray-400">
              Use <kbd className="rounded bg-gray-700 px-1.5 py-0.5 font-mono">←</kbd>{' '}
              <kbd className="rounded bg-gray-700 px-1.5 py-0.5 font-mono">→</kbd> to navigate •{' '}
              <kbd className="rounded bg-gray-700 px-1.5 py-0.5 font-mono">Esc</kbd> to close
            </div>
          </DialogPrimitive.Content>
        </DialogPortal>
      </Dialog>
    </>
  );
});
