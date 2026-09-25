<template>
  <v-dialog transition="dialog-bottom-transition" width="700" persistent>
    <v-card class="rounded-xl">
      <v-card-title class="d-flex align-center py-3 px-4 bg-deep-purple-darken-3 text-white">
        <v-icon icon="mdi-shield-vpn" class="mr-2"></v-icon>
        <span class="text-h6 font-weight-bold">ProtonVPN 节点中心与自动同步</span>
        <v-spacer></v-spacer>
        <v-btn icon="mdi-close" variant="text" size="small" @click="closeModal"></v-btn>
      </v-card-title>

      <v-tabs v-model="tab" color="deep-purple-accent-3" align-tabs="center">
        <v-tab value="account">
          <v-icon start icon="mdi-account-sync"></v-icon>
          账号一键同步
        </v-tab>
        <v-tab value="directory">
          <v-icon start icon="mdi-folder-open"></v-icon>
          本地/服务器目录导入
        </v-tab>
      </v-tabs>

      <v-card-text class="pt-4">
        <v-window v-model="tab">
          <!-- Tab 1: Account Login & Sync -->
          <v-window-item value="account">
            <v-alert type="info" variant="tonal" class="mb-4 text-body-2" density="compact">
              输入您的 Proton 账号信息，系统将通过模拟浏览器全自动登录、自动剔除失效节点、并将可用出口编排进 <code>us-pool</code> 等智能竞速池中。
            </v-alert>

            <v-row dense>
              <v-col cols="12">
                <v-text-field
                  v-model="form.username"
                  label="Proton 用户名 / 注册邮箱"
                  prepend-inner-icon="mdi-account"
                  placeholder="例如 username 或 user@proton.me"
                  variant="outlined"
                  density="comfortable"
                  hide-details="auto"
                ></v-text-field>
              </v-col>

              <v-col cols="12" class="mt-2">
                <v-text-field
                  v-model="form.password"
                  :type="showPassword ? 'text' : 'password'"
                  label="Proton 登录密码"
                  prepend-inner-icon="mdi-lock"
                  :append-inner-icon="showPassword ? 'mdi-eye-off' : 'mdi-eye'"
                  @click:append-inner="showPassword = !showPassword"
                  placeholder="输入您的 Proton 密码"
                  variant="outlined"
                  density="comfortable"
                  hide-details="auto"
                ></v-text-field>
              </v-col>

              <v-col cols="12" class="mt-2">
                <v-select
                  v-model="form.countries"
                  :items="countryOptions"
                  item-title="title"
                  item-value="value"
                  label="目标出口国家 (多选)"
                  prepend-inner-icon="mdi-earth"
                  multiple
                  chips
                  closable-chips
                  variant="outlined"
                  density="comfortable"
                  hide-details="auto"
                ></v-select>
              </v-col>

              <v-col cols="12" class="mt-2">
                <v-switch
                  v-model="form.headless"
                  color="deep-purple-accent-3"
                  label="后台静默执行 (若遇到复杂验证码，可关闭此开关弹出可视化浏览器窗口)"
                  density="compact"
                  hide-details
                ></v-switch>
              </v-col>
            </v-row>
          </v-window-item>

          <!-- Tab 2: Directory Scanning & Import -->
          <v-window-item value="directory">
            <v-alert type="info" variant="tonal" class="mb-4 text-body-2" density="compact">
              直接递归扫描服务器或本地存储 WireGuard <code>.conf</code> 配置文件的目录，自动精准推断国家归属并生成纯用户态出口端点。
            </v-alert>

            <v-row dense>
              <v-col cols="12">
                <v-text-field
                  v-model="dirForm.directory"
                  label="配置文件存放目录路径"
                  prepend-inner-icon="mdi-folder-search-outline"
                  placeholder="例如 C:\Users\acer\Downloads\openvpn\ 或 /root/openvpn/"
                  variant="outlined"
                  density="comfortable"
                  hide-details="auto"
                ></v-text-field>
              </v-col>
            </v-row>
          </v-window-item>
        </v-window>

        <!-- Feedback Alert -->
        <v-alert
          v-if="statusMessage"
          :type="statusType"
          variant="tonal"
          class="mt-4"
          density="comfortable"
        >
          <div class="font-weight-medium">{{ statusTitle }}</div>
          <div class="text-caption mt-1">{{ statusMessage }}</div>
        </v-alert>
      </v-card-text>

      <v-divider></v-divider>

      <v-card-actions class="px-4 py-3">
        <v-spacer></v-spacer>
        <v-btn variant="outlined" color="grey" @click="closeModal" :disabled="loading">
          取消
        </v-btn>

        <template v-if="tab === 'account'">
          <v-btn
            color="deep-purple-darken-2"
            variant="flat"
            prepend-icon="mdi-cloud-sync"
            :loading="loading"
            @click="syncProtonAccount"
          >
            开始同步 Proton 节点
          </v-btn>
        </template>

        <template v-else>
          <v-btn
            color="primary"
            variant="flat"
            prepend-icon="mdi-folder-download"
            :loading="loading"
            @click="importDirectory"
          >
            一键扫描并导入
          </v-btn>
        </template>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script lang="ts">
import HttpUtils from '@/plugins/httputil'
import Data from '@/store/modules/data'

export default {
  props: {
    visible: {
      type: Boolean,
      default: false
    }
  },
  emits: ['close'],
  data() {
    return {
      tab: 'account',
      loading: false,
      showPassword: false,
      form: {
        username: '',
        password: '',
        headless: true,
        countries: ['US', 'JP', 'NL']
      },
      dirForm: {
        directory: 'C:\\Users\\acer\\Downloads\\openvpn\\'
      },
      countryOptions: [
        { title: '🇺🇸 美国 (United States)', value: 'US' },
        { title: '🇯🇵 日本 (Japan)', value: 'JP' },
        { title: '🇳🇱 荷兰 (Netherlands)', value: 'NL' },
        { title: '🇸🇬 新加坡 (Singapore)', value: 'SG' }
      ],
      statusMessage: '',
      statusTitle: '',
      statusType: 'info' as 'info' | 'success' | 'warning' | 'error'
    }
  },
  methods: {
    async syncProtonAccount() {
      this.loading = true
      this.statusMessage = '正在连接 Proton 门户并执行全自动收割，请稍候...'
      this.statusTitle = '同步进行中'
      this.statusType = 'info'

      try {
        const payload = {
          username: this.form.username,
          password: this.form.password,
          headless: this.form.headless,
          countries: this.form.countries
        }

        const res: any = await HttpUtils.post('api/protonAutoHarvest', payload)
        if (res && res.success) {
          this.statusType = 'success'
          this.statusTitle = '同步完成'
          this.statusMessage = res.msg || 'ProtonVPN 节点已成功获取并导入智能竞速池！'
          await Data().loadData()
        } else {
          this.statusType = 'error'
          this.statusTitle = '同步失败'
          this.statusMessage = res?.msg || '未知错误，请检查账号密码或关闭静默开关尝试'
        }
      } catch (err: any) {
        this.statusType = 'error'
        this.statusTitle = '请求异常'
        this.statusMessage = err?.message || String(err)
      } finally {
        this.loading = false
      }
    },

    async importDirectory() {
      if (!this.dirForm.directory.trim()) return

      this.loading = true
      this.statusMessage = '正在扫描指定目录并提取 WireGuard 节点...'
      this.statusTitle = '扫描中'
      this.statusType = 'info'

      try {
        const res: any = await HttpUtils.post('api/protonImportDir', {
          directory: this.dirForm.directory
        })

        if (res && res.success) {
          this.statusType = 'success'
          this.statusTitle = '导入成功'
          const obj = res.obj || {}
          const details = Object.entries(obj)
            .map(([c, count]) => `[${c}]: ${count} 节点`)
            .join(', ')
          this.statusMessage = `成功导入配置：${details || '已刷新出口池'}`
          await Data().loadData()
        } else {
          this.statusType = 'error'
          this.statusTitle = '导入失败'
          this.statusMessage = res?.msg || '扫描目录失败，请检查路径权限'
        }
      } catch (err: any) {
        this.statusType = 'error'
        this.statusTitle = '请求异常'
        this.statusMessage = err?.message || String(err)
      } finally {
        this.loading = false
      }
    },

    closeModal() {
      this.statusMessage = ''
      this.$emit('close')
    }
  },
  watch: {
    visible(val) {
      if (val) {
        this.statusMessage = ''
      }
    }
  }
}
</script>
