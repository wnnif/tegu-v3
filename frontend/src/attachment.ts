export interface AttachmentUpload { name: string; content_base64: string }

export async function readAttachment(file: File): Promise<AttachmentUpload> {
  if (!file.size || file.size > 2 * 1024 * 1024) throw new Error('附件不能为空，且不能超过2 MB')
  if (!file.name || file.name.length > 200) throw new Error('附件文件名不能超过200个字符')
  const dataURL = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(new Error('读取附件失败，请重新选择'))
    reader.readAsDataURL(file)
  })
  const encoded = dataURL.split(',', 2)[1]
  if (!encoded) throw new Error('读取附件失败，请重新选择')
  return { name: file.name, content_base64: encoded }
}

export function savedAttachmentLink(value?: string): string {
  const match = /^file:([0-9a-f]{32})$/.exec(value || '')
  if (match) return `/api/attachment-file?id=${match[1]}`
  if (!value) return ''
  try { const url = new URL(value); return ['http:', 'https:'].includes(url.protocol) ? url.href : '' }
  catch { return '' }
}
