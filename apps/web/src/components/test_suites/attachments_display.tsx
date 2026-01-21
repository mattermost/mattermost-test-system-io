import type { TestAttachment } from '../../types';
import { ScreenshotGallery } from '../ui/screenshot-gallery';

interface AttachmentsDisplayProps {
  attachments?: TestAttachment[];
}

/** Display attachments (screenshots) for a test result */
export function AttachmentsDisplay({ attachments }: AttachmentsDisplayProps) {
  if (!attachments || attachments.length === 0) return null;

  // Filter to only show image attachments that have s3_key (found in storage)
  const imageAttachments = attachments.filter(
    (a) => a.content_type?.startsWith('image/') && a.s3_key && !a.missing
  );

  if (imageAttachments.length === 0) return null;

  // Sort by sequence to preserve original JUnit XML order
  const sortedAttachments = [...imageAttachments].sort((a, b) => a.sequence - b.sequence);

  return (
    <div className="ml-5 mt-2">
      <p className="text-xs text-gray-500 dark:text-gray-400 mb-1">
        Screenshots ({sortedAttachments.length})
      </p>
      <ScreenshotGallery screenshots={sortedAttachments} />
    </div>
  );
}
