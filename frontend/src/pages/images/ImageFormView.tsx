import type { ReactNode } from 'react'
import type {
  Control,
  FieldErrors,
  UseFormRegister,
  UseFormSetValue,
  UseFormWatch,
} from 'react-hook-form'
import { Link, useNavigate } from 'react-router-dom'
import type { ImageFormValues } from './ImageDetail.types'
import type { Source } from './ToolSourceSidebar'
import { Button } from '../../primitives/Button'
import { ConfirmModal } from '../../primitives/ConfirmModal'
import { Field } from '../../primitives/Field'
import { Input } from '../../primitives/Input'
import { Notice } from '../../primitives/Notice'
import { Panel } from '../../primitives/Panel'
import { Titleblock } from '../../layout/Titleblock'
import { EnvVarFieldArray } from './EnvVarFieldArray'
import { ToolFieldArray } from './ToolFieldArray'
import { McpServerSelector } from './McpServerSelector'
import { GroupField } from '../../components/GroupField'
import { ManagedBadge } from '../../components/ManagedBadge'

export type ImageFormViewProps = {
  isEdit: boolean
  id: string | undefined
  /** Embedded mode: no page titleblock, no Cancel/Delete — just the form. */
  embedded?: boolean
  /** Which form sections to render. Default 'all'; the agent detail tabs use
   *  the narrower modes. The MCP server picker is part of 'all' only — the
   *  agent detail page has its own agent-scoped MCP section, so the shared
   *  image picker never appears there. Skills are not part of an image: they
   *  are selected per agent. */
  sections?: 'all' | 'image' | 'tools'
  image: { displayName?: string; imageURL?: string; managedBy?: string } | undefined
  register: UseFormRegister<ImageFormValues>
  control: Control<ImageFormValues>
  setValue: UseFormSetValue<ImageFormValues>
  watch: UseFormWatch<ImageFormValues>
  errors: FieldErrors<ImageFormValues>
  isSubmitting: boolean
  onSubmit: (e: React.FormEvent) => void
  onRefreshMCP: () => Promise<void>
  onConfirmDelete: () => void
  submitError: string | null
  mcpWarnings: string[]
  mcpRefreshResult: string | null
  selectedIndex: number | null
  setSelectedIndex: (index: number | null) => void
  activeSource: Source
  setActiveSource: (source: Source) => void
  confirmOpen: boolean
  setConfirmOpen: (open: boolean) => void
  isRefreshing: boolean
  isSaving: boolean
  isDeleting: boolean
  /** Externally managed image (issue #146): the form is view-only — no save,
   *  delete, or MCP refresh, which the hub refuses with 409 anyway. */
  readOnly?: boolean
  /** Second zone of the merged Environment card (embedded 'image' mode):
   *  the agent's own override editor, with its own separate save path. */
  agentEnvSlot?: ReactNode
}

export function ImageFormView({
  isEdit,
  id,
  embedded = false,
  sections = 'all',
  image,
  register,
  control,
  setValue,
  watch,
  errors,
  isSubmitting,
  onSubmit,
  onRefreshMCP,
  onConfirmDelete,
  submitError,
  mcpWarnings,
  mcpRefreshResult,
  selectedIndex,
  setSelectedIndex,
  activeSource,
  setActiveSource,
  confirmOpen,
  setConfirmOpen,
  isRefreshing,
  isSaving,
  isDeleting,
  readOnly = false,
  agentEnvSlot,
}: ImageFormViewProps) {
  const navigate = useNavigate()
  const mcpServers = watch('mcpServers') ?? []
  const envVars = watch('env') ?? []

  return (
    <>
      {embedded ? (
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'baseline',
            gap: 12,
            marginBottom: 16,
          }}
        >
          <div className="label" style={{ fontSize: 13 }}>
            {image?.displayName ?? id ?? 'Image'} <ManagedBadge managedBy={image?.managedBy} />
          </div>
          {readOnly ? null : (
            <Button type="submit" variant="primary" form="image-form" disabled={isSubmitting}>
              {isSubmitting ? 'Saving…' : 'Save'}
            </Button>
          )}
        </div>
      ) : (
        <Titleblock
          crumbs={
            <>
              Library / <Link to="/agent-images">Agent Images</Link> /{' '}
              <b>{isEdit ? (image?.displayName ?? id) : 'New'}</b>
            </>
          }
          title={
            <>
              {isEdit ? (image?.displayName ?? <em>Image</em>) : 'New '}
              {!isEdit ? <em>Image</em> : null} <ManagedBadge managedBy={image?.managedBy} />
            </>
          }
          actions={
            <>
              <Button onClick={() => navigate(-1)}>Cancel</Button>
              {readOnly ? null : (
                <Button type="submit" variant="primary" form="image-form" disabled={isSubmitting}>
                  {isSubmitting ? 'Saving…' : isEdit ? 'Save' : 'Create'}
                </Button>
              )}
              {isEdit && !readOnly ? (
                <Button variant="danger" onClick={() => setConfirmOpen(true)}>
                  Delete
                </Button>
              ) : null}
            </>
          }
        />
      )}
      <form
        id="image-form"
        onSubmit={onSubmit}
        noValidate
        style={
          embedded
            ? { display: 'grid', gap: 24, maxWidth: 1100 }
            : { display: 'grid', gap: 24, padding: '28px 32px', maxWidth: 1100 }
        }
      >
        {submitError ? <Notice variant="err">{submitError}</Notice> : null}
        {mcpWarnings.length > 0 ? (
          <Notice variant="warn">
            <div>
              <strong>MCP refresh warnings (some servers may not have been reached):</strong>
              <ul>
                {mcpWarnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </div>
          </Notice>
        ) : null}
        {mcpRefreshResult ? <Notice>{mcpRefreshResult}</Notice> : null}

        {sections === 'all' || sections === 'image' ? (
          <Panel title="Image" className="cropped">
            <div style={{ display: 'grid', gap: 14 }}>
              <Field label="Display Name" htmlFor="displayName" error={errors.displayName?.message}>
                <Input id="displayName" {...register('displayName')} />
              </Field>
              {!isEdit ? (
                <GroupField
                  value={watch('groupId') ?? ''}
                  onChange={(v) => setValue('groupId', v, { shouldDirty: true })}
                  error={errors.groupId?.message}
                />
              ) : null}
              <Field label="Image URL" htmlFor="imageURL" error={errors.imageURL?.message}>
                <Input
                  id="imageURL"
                  placeholder="ghcr.io/org/image:tag"
                  {...register('imageURL')}
                />
              </Field>
              <Field label="Description" htmlFor="description">
                <Input id="description" {...register('description')} />
              </Field>
            </div>
          </Panel>
        ) : null}

        {sections === 'all' || sections === 'image' ? (
          embedded ? (
            /* Merged Environment card (issue #215): one card, two zones with
               separate save paths — the image env saves with the image Save
               above; the agent overrides slot saves on its own. */
            <Panel title="Environment">
              <div style={{ display: 'grid', gap: 20 }}>
                <div style={{ display: 'grid', gap: 8 }}>
                  <div className="label" style={{ fontSize: 13 }}>
                    Image environment
                  </div>
                  <p className="label" style={{ margin: 0, color: 'var(--ink-3)' }}>
                    Edits the shared image: saving with the image Save above updates this
                    environment for every agent that references it.
                  </p>
                  <EnvVarFieldArray
                    bare
                    control={control}
                    register={register}
                    watch={watch}
                    errors={errors}
                  />
                </div>
                {agentEnvSlot}
              </div>
            </Panel>
          ) : (
            <EnvVarFieldArray control={control} register={register} watch={watch} errors={errors} />
          )
        ) : null}

        {sections === 'all' ? (
          <McpServerSelector
            mcpServers={mcpServers}
            envNames={envVars.map((e) => e.name).filter((n) => !!n)}
            onChange={(names) => setValue('mcpServers', names)}
            refresh={
              isEdit && id && !readOnly
                ? {
                    id,
                    onClick: onRefreshMCP,
                    isPending: isRefreshing,
                    isSaving,
                  }
                : undefined
            }
          />
        ) : null}

        {sections === 'all' || sections === 'tools' ? (
          <ToolFieldArray
            control={control}
            register={register}
            setValue={setValue}
            watch={watch}
            selectedIndex={selectedIndex}
            setSelectedIndex={setSelectedIndex}
            activeSource={activeSource}
            setActiveSource={setActiveSource}
            onRefreshMCP={onRefreshMCP}
            isRefreshing={isRefreshing}
            canRefresh={isEdit && !!id && !readOnly}
          />
        ) : null}
      </form>

      {embedded ? null : (
        <ConfirmModal
          open={confirmOpen}
          title="Delete agent image?"
          body={
            <>
              <b>{image?.displayName ?? id}</b> will be permanently removed. Agents referencing this
              image will fail to start until you point them at a replacement.
            </>
          }
          confirmLabel={isDeleting ? 'Deleting…' : 'Delete'}
          destructive
          onConfirm={onConfirmDelete}
          onCancel={() => setConfirmOpen(false)}
        />
      )}
    </>
  )
}
