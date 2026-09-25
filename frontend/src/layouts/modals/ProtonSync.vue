<template>
  <v-dialog transition="dialog-bottom-transition" width="760" persistent>
    <v-card class="rounded-xl overflow-hidden">
      <v-card-title class="d-flex align-center py-3 px-4 bg-deep-purple-darken-3 text-white">
        <v-icon icon="mdi-shield-vpn" class="mr-2"></v-icon>
        <span class="text-h6 font-weight-bold">ProtonVPN 节点中心与一键导入</span>
        <v-spacer></v-spacer>
        <v-btn icon="mdi-close" variant="text" size="small" @click="closeModal"></v-btn>
      </v-card-title>

      <v-tabs v-model="tab" color="deep-purple-accent-3" align-tabs="center" class="border-b">
        <v-tab value="upload">
          <v-icon start icon="mdi-upload-multiple"></v-icon>
          文件上传 / 拖拽导入 .conf
        </v-tab>
        <v-tab value="paste">
          <v-icon start icon="mdi-content-paste"></v-icon>
          配置文本批量粘贴
        </v-tab>
        <v-tab value="account">
          <v-icon start icon="mdi-account-sync"></v-icon>
          Proton 账号自动同步
        </v-tab>
        <v-tab value="directory">
          <v-icon start icon="mdi-folder-open"></v-icon>
          服务器目录扫描
        </v-tab>
      </v-tabs>

      <v-card-text class="pt-4 px-5">
        <v-window v-model="tab">
          <!-- Tab 1: File Upload & Drag & Drop (PRIMARY) -->
          <v-window-item value="upload">
            <v-alert type="info" variant="tonal" class="mb-4 text-body-2" density="compact">
              从本地电脑批量选择或拖拽 WireGuard <code>.conf</code> 配置文件（如 <code>sui-node-US-FREE-9.conf</code> 等）。系统自动解析密钥、推断国家归属、创建纯用户态出口端点并编排进 <code>us-pool</code> 等策略组，<b>100% 成功且秒级生效</b>！
            </v-alert>

            <!-- Drag & Drop Zone -->
            <div
              class="dropzone pa-6 text-center rounded-lg border-dashed mb-4"
              :class="{ 'dropzone-active': isDragging }"
              @dragover.prevent="isDragging = true"
              @dragleave.prevent="isDragging = false"
              @drop.prevent="handleDrop"
              @click="triggerFileInput"
              style="cursor: pointer; border: 2px dashed #9c27b0; background: rgba(156, 39, 176, 0.04); transition: all 0.2s;"
            >
              <input
                type="file"
                ref="fileInputRef"
                multiple
                accept=".conf"
                style="display: none"
                @change="handleFileChange"
              />
              <v-icon icon="mdi-cloud-upload" size="48" color="deep-purple-accent-3" class="mb-2"></v-icon>
              <div class="text-subtitle-1 font-weight-bold text-deep-purple-darken-2">
                点击选取或将 .conf 文件拖拽至此处
              </div>
              <div class="text-caption text-grey-darken-1 mt-1">
                支持同时选取多个 .conf 配置文件，系统将自动识别文件名或内容中的国家标签 (US / JP / NL / SG)
              </div>
            </div>

            <!-- Selected Files List -->
            <div v-if="selectedFiles.length > 0" class="mb-4">
              <div class="d-flex align-center justify-space-between mb-2">
                <span class="text-subtitle-2 font-weight-bold">
                  已选择 {{ selectedFiles.length }} 个配置文件：
                </span>
                <v-btn variant="text" size="x-small" color="error" @click="selectedFiles = []">
                  清空列表
                </v-btn>
              </div>

              <v-sheet border rounded class="pa-2 overflow-y-auto" style="max-height: 180px;">
                <v-list density="compact" class="py-0">
                  <v-list-item
                    v-for="(file, idx) in selectedFiles"
                    :key="idx"
                    class="px-2 rounded mb-1 bg-grey-lighten-4"
                  >
                    <template #prepend>
                      <v-icon icon="mdi-file-cog" size="small" color="deep-purple"></v-icon>
                    </template>
                    <v-list-item-title class="text-body-2 font-weight-medium">
                      {{ file.name }}
                      <span class="text-caption text-grey ml-2">({{ formatFileSize(file.size) }})</span>
                    </v-list-item-title>
                    <template #append>
                      <v-chip size="x-small" color="deep-purple" variant="flat" class="mr-2">
                        {{ getCountryBadge(file.name) }}
                      </v-chip>
                      <v-btn
                        icon="mdi-close"
                        variant="text"
                        size="x-small"
                        color="grey"
                        @click.stop="removeFile(idx)"
                      ></v-btn>
                    </template>
                  </v-list-item>
                </v-list>
              </v-sheet>
            </div>

            <!-- Country Override Option -->
            <v-row dense class="mt-2">
              <v-col cols="12">
                <v-select
                  v-model="uploadCountry"
                  :items="countryOverrideOptions"
                  item-title="title"
                  item-value="value"
                  label="国家归属识别模式"
                  prepend-inner-icon="mdi-earth"
                  variant="outlined"
                  density="comfortable"
                  hide-details
                ></v-select>
              </v-col>
            </v-row>
          </v-window-item>

          <!-- Tab 2: Batch Text Paste -->
          <v-window-item value="paste">
            <v-alert type="info" variant="tonal" class="mb-4 text-body-2" density="compact">
              直接粘贴 WireGuard <code>.conf</code> 文本内容。支持粘贴单个节点或连续多个包含 <code>[Interface]</code> 与 <code>[Peer]</code> 的配置块。
            </v-alert>

            <v-textarea
              v-model="pasteContent"
              label="WireGuard 配置文本内容"
              placeholder="[Interface]&#10;PrivateKey = yBVl8qcgy/OTwV7fZ4bQzeQv5OAR3AJ2C583nN5u218=&#10;Address = 10.2.0.2/32, 2a07:b944::2:2/128&#10;&#10;[Peer]&#10;# US-FREE#3&#10;PublicKey = bOz7aS+OtfmIiGLlQmnHrWb+wzw5qFp6PKdWPRlVORc=&#10;Endpoint = 195.181.163.1:51820"
              variant="outlined"
              density="comfortable"
              rows="7"
              class="font-monospace text-body-2"
              hide-details="auto"
            ></v-textarea>

            <v-row dense class="mt-3">
              <v-col cols="12">
                <v-select
                  v-model="pasteCountry"
                  :items="countryOptions"
                  item-title="title"
                  item-value="value"
                  label="目标归属国家 (未包含国家注释时生效)"
                  prepend-inner-icon="mdi-flag"
                  variant="outlined"
                  density="comfortable"
                  hide-details
                ></v-select>
              </v-col>
            </v-row>
          </v-window-item>

          <!-- Tab 3: Account Login & Sync -->
          <v-window-item value="account">
            <v-alert type="warning" variant="tonal" class="mb-4 text-body-2" density="compact">
              Proton 官方接口对公网云服务器 IP 实行严格人机验证 (CAPTCHA)。在 Linux 云服务器上运行，<b>强烈推荐优先使用第 1 个【文件上传】标签页</b>导入本地下载好的 .conf 文件，100% 成功且不受验证码拦截。
            </v-alert>

            <v-row dense>
              <v-col cols="12">
                <v-text-field
                  v-model="form.username"
                  label="Proton 用户名 / 注册邮箱"
                  prepend-inner-icon="mdi-account"
                  placeholder="例如 dshymail@gmail.com"
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
                  label="后台静默执行 (仅在具备图形桌面与 Chrome 的环境中有效)"
                  density="compact"
                  hide-details
                ></v-switch>
              </v-col>
            </v-row>
          </v-window-item>

          <!-- Tab 4: Directory Scanning & Import -->
          <v-window-item value="directory">
            <v-alert type="info" variant="tonal" class="mb-4 text-body-2" density="compact">
              递归扫描服务器本地已存储的配置目录（例如服务器上的 <code>/usr/local/s-ui/configs/</code>）。
            </v-alert>

            <v-row dense>
              <v-col cols="12">
                <v-text-field
                  v-model="dirForm.directory"
                  label="服务器端配置文件存放路径"
                  prepend-inner-icon="mdi-folder-search-outline"
                  placeholder="例如 /usr/local/s-ui/configs/ 或 /root/openvpn/"
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
          <div class="text-caption mt-1" style="white-space: pre-line;">{{ statusMessage }}</div>
        </v-alert>
      </v-card-text>

      <v-divider></v-divider>

      <v-card-actions class="px-5 py-3">
        <v-spacer></v-spacer>
        <v-btn variant="outlined" color="grey" @click="closeModal" :disabled="loading">
          取消
        </v-btn>

        <!-- Upload Button -->
        <template v-if="tab === 'upload'">
          <v-btn
            color="deep-purple-darken-2"
            variant="flat"
            prepend-icon="mdi-cloud-upload"
            :loading="loading"
            :disabled="selectedFiles.length === 0"
            @click="uploadFiles"
          >
            立即上传并编排进策略组 ({{ selectedFiles.length }})
          </v-btn>
        </template>

        <!-- Paste Button -->
        <template v-else-if="tab === 'paste'">
          <v-btn
            color="deep-purple-darken-2"
            variant="flat"
            prepend-icon="mdi-check-all"
            :loading="loading"
            :disabled="!pasteContent.trim()"
            @click="pasteConfigs"
          >
            立即解析并导入
          </v-btn>
        </template>

        <!-- Account Button -->
        <template v-else-if="tab === 'account'">
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

        <!-- Directory Button -->
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
      tab: 'upload',
      loading: false,
      isDragging: false,
      showPassword: false,
      selectedFiles: [] as File[],
      uploadCountry: 'AUTO',
      pasteContent: '',
      pasteCountry: 'US',
      form: {
        username: 'dshymail@gmail.com',
        password: '',
        headless: true,
        countries: ['US', 'JP', 'NL', 'SG']
      },
      dirForm: {
        directory: '/usr/local/s-ui/configs/'
      },
      countryOverrideOptions: [
        { title: '智能自动识别文件名与注释 (推荐)', value: 'AUTO' },
        { title: '🇺🇸 全部归入美国出口池 (us-pool)', value: 'US' },
        { title: '🇯🇵 全部归入日本出口池 (jp-pool)', value: 'JP' },
        { title: '🇳🇱 全部归入荷兰出口池 (nl-pool)', value: 'NL' },
        { title: '🇸🇬 全部归入新加坡出口池 (sg-pool)', value: 'SG' }
      ],
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
    triggerFileInput() {
      const input = this.$refs.fileInputRef as HTMLInputElement | undefined
      if (input) {
        input.click()
      }
    },

    handleFileChange(event: Event) {
      const target = event.target as HTMLInputElement
      if (target.files && target.files.length > 0) {
        this.addFiles(Array.from(target.files))
        target.value = ''
      }
    },

    handleDrop(event: DragEvent) {
      this.isDragging = false
      if (event.dataTransfer && event.dataTransfer.files && event.dataTransfer.files.length > 0) {
        this.addFiles(Array.from(event.dataTransfer.files))
      }
    },

    addFiles(files: File[]) {
      const confFiles = files.filter(f => f.name.toLowerCase().endsWith('.conf'))
      for (const file of confFiles) {
        if (!this.selectedFiles.some(existing => existing.name === file.name && existing.size === file.size)) {
          this.selectedFiles.push(file)
        }
      }
    },

    removeFile(index: number) {
      this.selectedFiles.splice(index, 1)
    },

    formatFileSize(bytes: number): string {
      if (bytes < 1024) return bytes + ' B'
      if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
      return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
    },

    getCountryBadge(filename: string): string {
      const upper = filename.toUpperCase()
      const match = upper.match(/(?:^|[^A-Z])(US|JP|NL|SG|HK|UK|GB|DE|CA|AU|FR|CH)(?:[^A-Z]|$)/)
      if (match) {
        const code = match[1] === 'UK' ? 'GB' : match[1]
        const flags: Record<string, string> = {
          US: '🇺🇸 US',
          JP: '🇯🇵 JP',
          NL: '🇳🇱 NL',
          SG: '🇸🇬 SG',
          HK: '🇭🇰 HK',
          GB: '🇬🇧 GB',
          DE: '🇩🇪 DE',
          CA: '🇨🇦 CA',
          AU: '🇦🇺 AU',
          FR: '🇫🇷 FR',
          CH: '🇨🇭 CH'
        }
        return flags[code] || code
      }
      if (upper.includes('JAPAN')) return '🇯🇵 JP'
      if (upper.includes('NETHERLANDS')) return '🇳🇱 NL'
      if (upper.includes('SINGAPORE')) return '🇸🇬 SG'
      return '🇺🇸 US'
    },

    async uploadFiles() {
      if (this.selectedFiles.length === 0) return
      this.loading = true
      this.statusMessage = '正在读取文件并在服务端解析注入 Sing-Box 出口池...'
      this.statusTitle = '正在处理'
      this.statusType = 'info'

      try {
        const fileItems = await Promise.all(
          this.selectedFiles.map(async (file: File) => {
            const text = await file.text()
            return {
              name: file.name,
              content: text,
              country: this.uploadCountry === 'AUTO' ? '' : this.uploadCountry
            }
          })
        )

        const res: any = await HttpUtils.post('api/protonUploadConfs', {
          files: fileItems,
          default_country: this.uploadCountry === 'AUTO' ? 'US' : this.uploadCountry
        })

        if (res && res.success) {
          this.statusType = 'success'
          this.statusTitle = '导入成功'
          this.statusMessage = res.obj?.message || res.msg || '节点已成功导入并编排进负载均衡池！'
          this.selectedFiles = []
          await Data().loadData()
        } else {
          this.statusType = 'error'
          this.statusTitle = '导入失败'
          this.statusMessage = res?.msg || '导入失败，请检查配置文件是否有效'
        }
      } catch (err: any) {
        this.statusType = 'error'
        this.statusTitle = '处理异常'
        this.statusMessage = err?.message || String(err)
      } finally {
        this.loading = false
      }
    },

    async pasteConfigs() {
      if (!this.pasteContent.trim()) return
      this.loading = true
      this.statusMessage = '正在解析 WireGuard 配置并导入出口池...'
      this.statusTitle = '正在处理'
      this.statusType = 'info'

      try {
        const res: any = await HttpUtils.post('api/protonUploadConfs', {
          raw_content: this.pasteContent,
          default_country: this.pasteCountry
        })

        if (res && res.success) {
          this.statusType = 'success'
          this.statusTitle = '导入成功'
          this.statusMessage = res.obj?.message || res.msg || '配置已成功导入！'
          this.pasteContent = ''
          await Data().loadData()
        } else {
          this.statusType = 'error'
          this.statusTitle = '导入失败'
          this.statusMessage = res?.msg || '解析配置失败，请确保格式正确'
        }
      } catch (err: any) {
        this.statusType = 'error'
        this.statusTitle = '处理异常'
        this.statusMessage = err?.message || String(err)
      } finally {
        this.loading = false
      }
    },

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
          this.statusMessage = res?.msg || '未知错误，建议直接使用【文件上传】导入本地 .conf 文件'
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
