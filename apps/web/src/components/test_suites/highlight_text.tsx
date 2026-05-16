interface HighlightTextProps {
  text: string;
  search: string;
}

export function HighlightText({ text, search }: HighlightTextProps) {
  if (!search || !text) {
    return <>{text}</>;
  }

  const lowerText = text.toLowerCase();
  const lowerSearch = search.toLowerCase();
  const index = lowerText.indexOf(lowerSearch);

  if (index === -1) {
    return <>{text}</>;
  }

  const before = text.slice(0, index);
  const match = text.slice(index, index + search.length);
  const after = text.slice(index + search.length);

  return (
    <>
      {before}
      <mark className="bg-yellow-200 text-yellow-900 dark:bg-yellow-500/40 dark:text-yellow-100">
        {match}
      </mark>
      {after && <HighlightText text={after} search={search} />}
    </>
  );
}
