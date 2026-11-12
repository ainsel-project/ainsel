import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useSkill, useSkills } from '../../api/skills'
import { ManagedBadge } from '../../components/ManagedBadge'
import { skillPath } from '../../components/entityPaths'
import { Markdown } from '../../primitives/Markdown'
import { DualListPicker } from '../../primitives/DualListPicker'
import { Panel } from '../../primitives/Panel'
import { Tag } from '../../primitives/Tag'

type SkillsSelectorProps = {
  /** The agent's current skill selection (skill ids). */
  selectedIds: string[]
  onChange: (ids: string[]) => void
  /**
   * Show that skill's body read-only below the picker and grow the row lists
   * into the available viewport height (issue #216).
   */
  previewable?: boolean
}

/**
 * The agent's own skill selection. Skills are not a property of the runtime
 * image: this picks from the hub's skill library and writes `spec.skills` on
 * the agent, so two agents on the same image can carry different skills.
 */
export function SkillsSelector({
  selectedIds,
  onChange,
  previewable = false,
}: SkillsSelectorProps) {
  const { data, isLoading } = useSkills({ pageSize: 200 })
  const [previewId, setPreviewId] = useState<string | null>(null)
  const skills = data?.items ?? []
  return (
    <>
      <Panel title="Skills">
        <DualListPicker
          items={skills}
          selectedIds={selectedIds}
          onChange={onChange}
          getId={(s) => s.id}
          getLabel={(s) => s.name}
          getDescription={(s) => s.description}
          isLoading={isLoading}
          emptyLabel="No skills available. Create skills first to enable them on this agent."
          enabledTitle="Enabled on this agent"
          onRowActivate={previewable ? setPreviewId : undefined}
          tall={previewable}
        />
      </Panel>
      {previewable && previewId ? <SkillPreview id={previewId} /> : null}
    </>
  )
}

/**
 * Read-only skill-body preview (issue #216), mirroring SkillDetail's
 * "Skill body" panel. The preview survives list mutations and search filters
 * — users often enable a skill while reading it — and is only replaced by
 * activating another row.
 */
function SkillPreview({ id }: { id: string }) {
  const { data, isLoading, error } = useSkill(id)
  return (
    <div aria-live="polite">
      <Panel
        title={data?.name ?? 'Skill'}
        right={
          <Link to={skillPath(id)} className="label" style={{ color: 'var(--signal-ink)' }}>
            Open skill →
          </Link>
        }
      >
        {isLoading ? <p className="label">Loading…</p> : null}
        {error ? (
          <p className="label" style={{ color: 'var(--signal)' }}>
            Failed to load skill.
          </p>
        ) : null}
        {data ? (
          <div style={{ display: 'grid', gap: 10 }}>
            <div className="label" style={{ margin: 0, color: 'var(--ink-3)' }}>
              {data.description || '—'}
              {data.tags && data.tags.length > 0 ? (
                <span style={{ display: 'inline-flex', gap: 4, flexWrap: 'wrap', marginLeft: 8 }}>
                  {data.tags.map((t) => (
                    <Tag key={t}>{t}</Tag>
                  ))}
                </span>
              ) : null}
              <span style={{ marginLeft: 8 }}>
                <ManagedBadge managedBy={data.managedBy} />
              </span>
            </div>
            {data.body ? (
              <Markdown source={data.body} />
            ) : (
              <p className="label">This skill has no body yet.</p>
            )}
          </div>
        ) : null}
      </Panel>
    </div>
  )
}
