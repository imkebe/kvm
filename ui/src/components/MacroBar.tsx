import { useEffect, useMemo } from "react";
import { LuCommand } from "react-icons/lu";

import { useMacrosStore, useSettingsStore } from "@hooks/stores";
import { MacroTagFilter, allMacroTags, macroMatches } from "@components/MacroTags";
import useKeyboard from "@hooks/useKeyboard";
import { useJsonRpc } from "@hooks/useJsonRpc";
import { Button } from "@components/Button";
import Container from "@components/Container";

export default function MacroBar() {
  const { macros, initialized, loadMacros, setSendFn } = useMacrosStore();
  const { executeMacro } = useKeyboard();
  const { send } = useJsonRpc();
  const macroBarTag = useSettingsStore(state => state.macroBarTag);
  const setMacroBarTag = useSettingsStore(state => state.setMacroBarTag);
  const tags = useMemo(() => allMacroTags(macros), [macros]);
  // A tag no longer in use shows all macros.
  const activeTag = macroBarTag && tags.includes(macroBarTag) ? macroBarTag : null;
  const shown = useMemo(
    () => macros.filter(macro => macroMatches(macro, activeTag)),
    [macros, activeTag],
  );

  useEffect(() => {
    setSendFn(send);

    if (!initialized) {
      loadMacros();
    }
  }, [initialized, loadMacros, setSendFn, send]);

  if (macros.length === 0) {
    return null;
  }

  return (
    <Container className="border-b border-b-slate-800/20 bg-white dark:border-b-slate-300/20 dark:bg-slate-900">
      <div className="flex items-center gap-x-2 py-1.5">
        <div className="absolute -ml-5">
          <LuCommand className="h-4 w-4 text-slate-500" />
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {tags.length > 0 && (
            <>
              <MacroTagFilter tags={tags} value={activeTag} onChange={setMacroBarTag} size="xs" />
              <div className="h-4 w-px bg-slate-800/20 dark:bg-slate-300/20" />
            </>
          )}
          {shown.map(macro => (
            <Button
              key={macro.id}
              aria-label={macro.name}
              size="XS"
              theme="light"
              text={macro.name}
              onClick={() => executeMacro(macro.steps)}
            />
          ))}
        </div>
      </div>
    </Container>
  );
}
