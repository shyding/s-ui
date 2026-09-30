<template>
  <v-dialog v-model="localVisible" width="700" persistent>
    <v-card class="rounded-lg">
      <!-- 标题栏 -->
      <v-card-title class="d-flex align-center py-4 px-5">
        <v-icon start color="teal">mdi-shield-check</v-icon>
        <span>节点健康检测</span>
        <v-spacer />
        <v-chip
          v-if="isRunning"
          color="warning"
          variant="tonal"
          size="small"
          prepend-icon="mdi-loading"
          class="mr-2"
        >检测中…</v-chip>
        <v-chip v-else color="teal" variant="tonal" size="small" prepend-icon="mdi-clock-outline">
          每日 {{ statusData.healthCheckTime || '03:30' }} 自动运行
        </v-chip>
      </v-card-title>
      <v-divider />

      <v-card-text class="px-5 pt-4 pb-2">
        <!-- CPU 保护提示 -->
        <v-alert type="info" variant="tonal" density="compact" class="mb-4" icon="mdi-cpu-64-bit">
          <strong>低并发保护</strong>：并发数 ≤ 5，不会引起系统 CPU 过高。
          手动触发与定时任务<strong>互斥</strong>，正在运行时禁止重复触发。
        </v-alert>

        <!-- ─── 定时计划配置 ─── -->
        <div class="text-subtitle-2 mb-2 d-flex align-center" style="gap:6px">
          <v-icon size="18" color="teal">mdi-calendar-clock</v-icon>
          每日自动检测时间
        </div>
        <v-row dense align="center">
          <v-col cols="12" sm="5">
            <v-text-field
              v-model="editTime"
              type="time"
              label="检测时间（24小时制）"
              density="compact"
              hide-details
              variant="outlined"
              prepend-inner-icon="mdi-clock-edit-outline"
            />
          </v-col>
          <v-col cols="12" sm="4">
            <v-btn
              color="teal"
              variant="tonal"
              :loading="savingTime"
              :disabled="editTime === statusData.healthCheckTime"
              @click="saveTime"
              prepend-icon="mdi-content-save"
            >
              保存时间设置
            </v-btn>
          </v-col>
          <v-col cols="12" sm="3">
            <div class="text-caption text-medium-emphasis">
              下次运行：{{ nextRunLabel }}
            </div>
          </v-col>
        </v-row>

        <v-divider class="my-4" />

        <!-- ─── 检测范围 ─── -->
        <div class="text-subtitle-2 mb-2 d-flex align-center" style="gap:6px">
          <v-icon size="18" color="teal">mdi-filter-check</v-icon>
          手动触发范围
        </div>
        <v-row dense>
          <v-col cols="12" sm="6">
            <v-checkbox
              v-model="scope.egress"
              label="出口节点（Proton / HProxy / Seed Egress）"
              density="compact"
              hide-details
              color="teal"
            />
          </v-col>
          <v-col cols="12" sm="6">
            <v-checkbox
              v-model="scope.nodeHealth"
              label="外部订阅节点（node_health_statuses）"
              density="compact"
              hide-details
              color="indigo"
            />
          </v-col>
        </v-row>

        <v-divider class="my-4" />

        <!-- ─── 实时状态卡片 ─── -->
        <div class="text-subtitle-2 mb-2 d-flex align-center" style="gap:6px">
          <v-icon size="18" color="teal">mdi-pulse</v-icon>
          实时状态
          <v-btn
            size="x-small"
            variant="text"
            icon="mdi-refresh"
            :loading="refreshing"
            @click="fetchStatus"
          />
        </div>
        <v-row dense>
          <v-col cols="6" sm="3">
            <v-card
              :color="statusData.egressCheckRunning ? 'warning' : 'teal'"
              variant="tonal"
              class="text-center pa-2"
            >
              <v-icon :icon="statusData.egressCheckRunning ? 'mdi-loading' : 'mdi-check-circle'" size="20" />
              <div class="text-caption mt-1">出口检测</div>
              <div class="text-body-2 font-weight-bold">
                {{ statusData.egressCheckRunning ? `${egressPercent}% (${statusData.egressDone}/${statusData.egressTotal})` : '空闲' }}
              </div>
              <v-progress-linear
                v-if="statusData.egressCheckRunning && statusData.egressTotal > 0"
                :model-value="egressPercent"
                color="warning"
                height="4"
                rounded
                class="mt-1"
              />
            </v-card>
          </v-col>
          <v-col cols="6" sm="3">
            <v-card
              :color="statusData.nodeCheckRunning ? 'warning' : 'indigo'"
              variant="tonal"
              class="text-center pa-2"
            >
              <v-icon :icon="statusData.nodeCheckRunning ? 'mdi-loading' : 'mdi-check-circle'" size="20" />
              <div class="text-caption mt-1">节点检测</div>
              <div class="text-body-2 font-weight-bold">
                {{ statusData.nodeCheckRunning ? `${nodePercent}% (${statusData.nodeDone}/${statusData.nodeTotal})` : '空闲' }}
              </div>
              <v-progress-linear
                v-if="statusData.nodeCheckRunning && statusData.nodeTotal > 0"
                :model-value="nodePercent"
                color="warning"
                height="4"
                rounded
                class="mt-1"
              />
            </v-card>
          </v-col>
          <v-col cols="12" sm="6">
            <v-card variant="tonal" color="grey" class="pa-3 h-100">
              <div class="text-caption text-medium-emphasis">并发限制</div>
              <div class="text-body-2">≤ 5 个并发连接（内置保护）</div>
              <div class="text-caption text-medium-emphasis mt-1">下次定时</div>
              <div class="text-body-2">每天 {{ statusData.healthCheckTime || '03:30' }}（服务器本地时间）</div>
            </v-card>
          </v-col>
        </v-row>

        <!-- 结果提示 -->
        <v-alert
          v-if="resultMsg"
          :type="resultType"
          variant="tonal"
          density="compact"
          class="mt-3"
          closable
          @click:close="resultMsg = ''"
        >{{ resultMsg }}</v-alert>
      </v-card-text>

      <v-card-actions class="px-5 pb-4">
        <v-btn color="grey" variant="text" @click="close" prepend-icon="mdi-close">关闭</v-btn>
        <v-spacer />
        <v-btn
          color="teal"
          variant="elevated"
          prepend-icon="mdi-play-circle"
          :loading="triggering"
          :disabled="isRunning || (!scope.egress && !scope.nodeHealth)"
          @click="triggerCheck"
        >
          {{ isRunning ? '检测中，请稍候…' : '立即手动检测' }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts">
import HttpUtils from '@/plugins/httputil'

export default {
  name: 'EgressHealthCheck',
  props: ['visible'],
  emits: ['close'],
  data() {
    return {
      localVisible: false,
      triggering: false,
      savingTime: false,
      refreshing: false,
      resultMsg: '',
      resultType: 'success' as 'success' | 'error' | 'info',
      editTime: '03:30',
      scope: { egress: true, nodeHealth: true },
      statusData: {
        egressCheckRunning: false,
        nodeCheckRunning: false,
        anyRunning: false,
        healthCheckTime: '03:30',
        nextSchedule: '每天 03:30 自动运行',
        egressDone: 0,
        egressTotal: 0,
        nodeDone: 0,
        nodeTotal: 0,
      },
      pollTimer: null as ReturnType<typeof setInterval> | null,
    }
  },
  computed: {
    isRunning(): boolean {
      return this.statusData.anyRunning
    },
    egressPercent(): number {
      const total = this.statusData.egressTotal || 0
      if (total === 0) return 0
      return Math.round((this.statusData.egressDone / total) * 100)
    },
    nodePercent(): number {
      const total = this.statusData.nodeTotal || 0
      if (total === 0) return 0
      return Math.round((this.statusData.nodeDone / total) * 100)
    },
    nextRunLabel(): string {
      // 计算距下次运行的小时数（简单估算）
      const t = this.editTime || this.statusData.healthCheckTime || '03:30'
      const [h, m] = t.split(':').map(Number)
      const now = new Date()
      const target = new Date()
      target.setHours(h, m, 0, 0)
      if (target <= now) target.setDate(target.getDate() + 1)
      const diffH = Math.round((target.getTime() - now.getTime()) / 3600000)
      return `约 ${diffH} 小时后`
    },
  },
  methods: {
    async fetchStatus() {
      this.refreshing = true
      try {
        const res: any = await HttpUtils.get('api/egressStatus')
        if (res?.obj) {
          this.statusData = { ...this.statusData, ...res.obj }
          // 同步时间选择器（仅在用户未在编辑时）
          if (this.statusData.healthCheckTime && this.editTime === this.statusData.healthCheckTime) {
            this.editTime = this.statusData.healthCheckTime
          }
        }
      } catch { /* ignore */ }
      this.refreshing = false
    },
    async saveTime() {
      this.savingTime = true
      this.resultMsg = ''
      try {
        const res: any = await HttpUtils.post('api/saveHealthCheckTime', {
          healthCheckTime: this.editTime,
        })
        if (res?.obj?.success) {
          this.statusData.healthCheckTime = this.editTime
          this.resultType = 'success'
          this.resultMsg = `✅ 已保存：每天 ${this.editTime} 自动运行（下次重启后生效）`
        } else {
          this.resultType = 'error'
          this.resultMsg = res?.msg || '保存失败'
        }
      } catch (e: any) {
        this.resultType = 'error'
        this.resultMsg = '保存失败：' + (e?.message || String(e))
      }
      this.savingTime = false
    },
    async triggerCheck() {
      this.triggering = true
      this.resultMsg = ''
      try {
        const res: any = await HttpUtils.post('api/egressHealthCheck', {
          egress: this.scope.egress ? '1' : '0',
          nodeHealth: this.scope.nodeHealth ? '1' : '0',
        })
        if (res?.obj?.success) {
          this.resultType = 'success'
          this.resultMsg = '✅ 检测任务已在后台启动（低并发，不影响正常服务）'
          await this.fetchStatus()
        } else if (res?.obj?.running === true && !res?.obj?.success) {
          this.resultType = 'info'
          this.resultMsg = '⚠️ 已有检测任务正在运行，请稍后再试'
        } else {
          this.resultType = 'error'
          this.resultMsg = res?.msg || '触发失败'
        }
      } catch (e: any) {
        if (e?.response?.status === 409) {
          this.resultType = 'info'
          this.resultMsg = '⚠️ 已有检测任务正在运行，请稍后再试'
        } else {
          this.resultType = 'error'
          this.resultMsg = '请求失败：' + (e?.message || String(e))
        }
      }
      this.triggering = false
    },
    startPolling() {
      this.fetchStatus()
      this.pollTimer = setInterval(this.fetchStatus, 5000)
    },
    stopPolling() {
      if (this.pollTimer) { clearInterval(this.pollTimer); this.pollTimer = null }
    },
    close() {
      this.stopPolling()
      this.$emit('close')
    },
  },
  watch: {
    visible(val: boolean) {
      this.localVisible = val
      if (val) {
        this.resultMsg = ''
        this.startPolling()
      } else {
        this.stopPolling()
      }
    },
    'statusData.healthCheckTime'(val: string) {
      // 首次加载后同步到编辑器
      if (val && this.editTime === '03:30') {
        this.editTime = val
      }
    },
  },
}
</script>
