import { ref, type Ref } from 'vue'
import { api } from '../api'
import type { BackupConfig } from './models'
import type { MaintenanceJob } from './readModels'

export const emptyBackupConfig = (): BackupConfig => ({ enabled: false, endpoint: '', region: 'us-east-1', bucket: '', prefix: 'tegu', access_key: '', secret: '', retain: 168, interval: 60 })

export function useMaintenance(perform: (fn: () => Promise<void>) => Promise<void>, notice: Ref<string>) {
  const backupConfig = ref<BackupConfig>(emptyBackupConfig())

  async function saveBackupConfig() {
    await perform(async () => {
      await api('/maintenance/settings', backupConfig.value)
      backupConfig.value = await api<BackupConfig>('/backup-settings')
      notice.value = '备份设置已保存；可点击立即备份验证云端上传'
    })
  }

  async function backup() {
    await perform(async () => {
      const job = await api<MaintenanceJob>('/maintenance/backup', {})
      notice.value = '备份任务已受理，编号 ' + job.id + '；下方任务记录可查看进度和结果'
    })
  }

  return { backupConfig, saveBackupConfig, backup }
}
