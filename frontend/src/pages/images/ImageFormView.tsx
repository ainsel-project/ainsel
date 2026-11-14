import type {
  Control,
  FieldErrors,
  UseFormRegister,
  UseFormSetValue,
  UseFormWatch,
} from 'react-hook-form'
import { useWatch } from 'react-hook-form'
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
import { EnvTable } from '../agents/workspace/EnvTable'
import { withDraftIds } from '../agents/workspace/envRows'
import { ToolFieldArray } from './ToolFieldArray'
import { McpServerSelector } from './McpServerSelector'
import { GroupField } from '../../components/GroupField'
import { ManagedBadge } from '../../components/ManagedBadge'

export type ImageFormViewProps = {
  isEdit: boolean
  id: string | undefined
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
}

/**
 * The image's own environment list, edited through the shared env table. The
 * form owns the rows (react-hook-field values), the table is controlled: each
 * change writes the whole list back with `setValue`. Draft ids are per-render
 * — the plain variant keeps entry order, so nothing depends on them.
 *
 * Secret round-trip: an existing secret row arrives with an empty value; an
 * unchanged empty value on save keeps the stored secret.
 */
function ImageEnvField({
  control,
  setValue,
  readOnly,
}: {
  control: Control<ImageFormValues>
  setValue: UseFormSetValue<ImageFormValues>
  readOnly: boolean
}) {
  const env = useWatch({ control, name: 'env' }) ?? []
  return (
    <EnvTable
      variant="plain"
      agentEnv={withDraftIds(env.map((e) => ({ ...e, secret: !!e.secret })))}
      readOnly={readOnly}
      onChange={(next) =>
        setValue(
          'env',
          next.map(({ name, value, secret }) => ({ name, value, secret: !!secret })),
          { shouldDirty: true },
        )
      }
    />
  )
}

export function ImageFormView({
  isEdit,
  id,
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
}: ImageFormViewProps) {
  const navigate = useNavigate()
  const mcpServers = watch('mcpServers') ?? []
  const envVars = watch('env') ?? []

  return (
    <>
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
      <form
        id="image-form"
        onSubmit={onSubmit}
        noValidate
        className="page"
        style={{ display: 'grid', gap: 24, maxWidth: 1100 }}
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
              <Input id="imageURL" placeholder="ghcr.io/org/image:tag" {...register('imageURL')} />
            </Field>
            <Field label="Description" htmlFor="description">
              <Input id="description" {...register('description')} />
            </Field>
          </div>
        </Panel>

        <Panel title="Environment Variables">
          <ImageEnvField control={control} setValue={setValue} readOnly={readOnly} />
        </Panel>

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
      </form>

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
    </>
  )
}
