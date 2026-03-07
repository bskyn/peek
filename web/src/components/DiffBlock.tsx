import { useMemo } from "react";
import { Diff, Hunk, parseDiff } from "react-diff-view";
import { structuredPatch } from "diff";

function buildUnifiedDiff(oldText: string, newText: string): string {
  const patch = structuredPatch("a", "b", oldText, newText, "", "", {
    context: 3,
  });

  if (patch.hunks.length === 0) return "";

  const lines: string[] = ["--- a", "+++ b"];

  for (const hunk of patch.hunks) {
    lines.push(
      `@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`,
    );
    for (const line of hunk.lines) {
      lines.push(line);
    }
  }

  return lines.join("\n") + "\n";
}

function safeParseDiff(diffText: string) {
  if (diffText === "") return [];
  try {
    return parseDiff(diffText);
  } catch {
    return [];
  }
}

type DiffBlockProps =
  | { rawDiff: string; editStrings?: never }
  | { rawDiff?: never; editStrings: { oldText: string; newText: string } | null };

export function DiffBlock(props: DiffBlockProps) {
  const files = useMemo(() => {
    if (props.rawDiff != null) {
      return safeParseDiff(props.rawDiff);
    }
    if (props.editStrings == null) return [];
    const { oldText, newText } = props.editStrings;
    if (oldText === newText) return [];
    return safeParseDiff(buildUnifiedDiff(oldText, newText));
  }, [props.rawDiff, props.editStrings]);

  // Fallback: show raw text if parsing fails
  if (files.length === 0 || files[0].hunks.length === 0) {
    const fallbackText =
      props.rawDiff ?? props.editStrings?.newText ?? "(empty)";
    if (fallbackText === "" || fallbackText === "(empty)") {
      return (
        <pre className="bg-mantle p-2.5 font-mono text-[11px] leading-relaxed text-subtext-0">
          (empty)
        </pre>
      );
    }
    // Render raw diff with line coloring as fallback
    return (
      <pre className="max-h-80 overflow-auto bg-mantle py-2.5 font-mono text-[11px] leading-relaxed">
        {fallbackText.split("\n").map((line, i) => {
          let color = "text-subtext-0";
          if (line.startsWith("+")) color = "text-green";
          else if (line.startsWith("-")) color = "text-red";
          else if (line.startsWith("@@")) color = "text-mauve";
          return (
            <div key={i} className={`px-2.5 ${color}`}>
              {line}
            </div>
          );
        })}
      </pre>
    );
  }

  return (
    <div className="diff-unified">
      {files.map((file) => (
        <Diff
          key={file.oldRevision + file.newRevision}
          viewType="unified"
          diffType={file.type}
          hunks={file.hunks}
        >
          {(hunks) =>
            hunks.map((hunk) => <Hunk key={hunk.content} hunk={hunk} />)
          }
        </Diff>
      ))}
    </div>
  );
}
