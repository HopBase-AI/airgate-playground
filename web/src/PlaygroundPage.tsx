import { useTranslation } from 'react-i18next';
import { cssVar } from '@doudou-start/airgate-theme';
import { PlaygroundProvider, usePlayground } from './playground/PlaygroundContext';
import { ChatRuntimeProvider } from './playground/aui/ChatRuntimeProvider';
import { ChatView } from './playground/ChatView';
import { styles, keyframes } from './playground/styles';

export function ChatPage() {
  return (
    <PlaygroundProvider>
      <ChatRuntimeProvider>
        <PlaygroundShell />
      </ChatRuntimeProvider>
    </PlaygroundProvider>
  );
}

export default ChatPage;

function PlaygroundShell() {
  const { t } = useTranslation();
  const {
    isMobile,
    sidebarOpen,
    setSidebarOpen,
    activeId,
    userInfo,
    sidebarConversations,
    createConversation,
    openConversation,
    deleteConversation,
  } = usePlayground();

  return (
    <div data-full-bleed data-pg-aesthetic style={styles.layout}>
      {sidebarOpen && isMobile && (
        <div style={styles.sidebarBackdrop} onClick={() => setSidebarOpen(false)} />
      )}

      <ImagePreviewOverlay />

      {sidebarOpen ? (
        <div style={{ ...styles.sidebar, ...(isMobile ? styles.sidebarMobile : null) }}>
          <div style={styles.sidebarHeader}>
            <div style={styles.sidebarTopbar}>
              <button
                style={styles.newBtn}
                className="pg-sidebar-action"
                onClick={createConversation}
                title={t('playground.new_conversation')}
                aria-label={t('playground.new_conversation')}
              >
                <span style={styles.newBtnIcon}>
                  <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round">
                    <path d="M7 1v12M1 7h12" />
                  </svg>
                </span>
                <span>{t('playground.new_conversation')}</span>
              </button>
              <button
                style={styles.toggleBtn}
                className="pg-sidebar-collapse-button"
                onClick={() => setSidebarOpen(false)}
                aria-label={t('playground.collapse_conversations')}
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M15 18l-6-6 6-6" />
                </svg>
              </button>
            </div>
          </div>

          <div style={styles.convList}>
            {sidebarConversations.map(conversation => {
              const isActive = conversation.id === activeId;
              return (
                <div
                  key={conversation.id}
                  className={`pg-conv-item${isActive ? ' is-active' : ''}`}
                  style={{ ...styles.convItem, ...(isActive ? styles.convItemActive : null) }}
                  onClick={() => openConversation(conversation.id)}
                >
                  <span style={{ ...styles.convIcon, color: isActive ? cssVar('text') : cssVar('textTertiary') }}>
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z" />
                    </svg>
                  </span>
                  <span style={{ ...styles.convTitle, color: isActive ? cssVar('text') : cssVar('textSecondary'), fontWeight: isActive ? 600 : 400 }}>
                    {conversation.title || t('playground.new_conversation')}
                  </span>
                  {isActive && <span aria-hidden="true" style={styles.convDot} />}
                  <button
                    type="button"
                    className="pg-conv-delete"
                    style={styles.deleteBtn}
                    onClick={(event) => { event.stopPropagation(); void deleteConversation(conversation.id); }}
                    title={t('playground.delete_conversation')}
                    aria-label={t('playground.delete_conversation')}
                  >
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M3 6h18" /><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" /><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" /><path d="M10 11v6" /><path d="M14 11v6" />
                    </svg>
                  </button>
                </div>
              );
            })}
            {sidebarConversations.length === 0 && (
              <div style={styles.emptyConvList}><span>{t('playground.no_conversations')}</span></div>
            )}
          </div>

          {userInfo && (
            <div style={styles.balanceBar}>
              <span style={styles.balanceLabel}>{t('playground.balance')}</span>
              <span style={styles.balanceValue}>${userInfo.balance.toFixed(4)}</span>
            </div>
          )}
        </div>
      ) : (
        <div style={{ ...styles.sidebarRail, ...(isMobile ? styles.sidebarRailMobile : null) }}>
          <button style={styles.toggleBtn} className="pg-sidebar-collapse-button" onClick={() => setSidebarOpen(true)} aria-label={t('playground.expand_conversations')}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round">
              <path d="M9 18l6-6-6-6" />
            </svg>
          </button>
        </div>
      )}

      <div style={styles.main}>
        <ChatView />
      </div>

      <style>{keyframes}</style>
    </div>
  );
}

function ImagePreviewOverlay() {
  const { t } = useTranslation();
  const { previewImage, setPreviewImage, showNextPreviewImage } = usePlayground();

  if (!previewImage) return null;
  const current = previewImage.images[previewImage.index] || previewImage.images[0];
  if (!current) return null;
  const hasNav = previewImage.images.length > 1;

  return (
    <div style={styles.imagePreviewOverlay} role="dialog" aria-modal="true" aria-label={current.alt || t('playground.image_preview')} onClick={() => setPreviewImage(null)}>
      <div style={styles.imagePreviewModal} onClick={event => event.stopPropagation()}>
        <img src={current.url} alt={current.alt} style={styles.imagePreviewLarge} />
        {hasNav && (
          <>
            <button type="button" style={{ ...styles.imagePreviewNavBtn, left: 12 }} onClick={() => showNextPreviewImage(-1)} aria-label={t('playground.previous_image')}>‹</button>
            <button type="button" style={{ ...styles.imagePreviewNavBtn, right: 12 }} onClick={() => showNextPreviewImage(1)} aria-label={t('playground.next_image')}>›</button>
            <div style={styles.imagePreviewCounter}>{previewImage.index + 1} / {previewImage.images.length}</div>
          </>
        )}
        <button type="button" style={styles.imagePreviewCloseBtn} onClick={() => setPreviewImage(null)} aria-label={t('playground.close_image_preview')}>×</button>
      </div>
    </div>
  );
}
