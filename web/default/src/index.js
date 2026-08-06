import React, { useEffect, useRef, useState } from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, useLocation } from 'react-router-dom';
import { Container } from 'semantic-ui-react';
import App from './App';
import Header from './components/Header';
import Footer from './components/Footer';
import 'semantic-ui-css/semantic.min.css';
import './index.css';
import { UserProvider } from './context/User';
import { ToastContainer } from 'react-toastify';
import 'react-toastify/dist/ReactToastify.css';
import { StatusProvider } from './context/Status';
import './i18n';

// dyt-59: 所有设备渲染同一张 1440px 固定画布。
// 外层：AppViewport 用 iframe 承载固定 1440 视口，再把 iframe 等比缩放铺满设备；
// 内层：iframe 地址带 canvas_inner 标记，只渲染完整应用（不再嵌套 iframe）。
// 媒体查询在内层永远按 1440 命中，各端看到的像素比例完全一致。
const CANVAS_WIDTH = 1440;

const IS_INNER = new URLSearchParams(window.location.search).has(
  'canvas_inner'
);

function getFrameScale() {
  return window.innerWidth / CANVAS_WIDTH;
}

function innerUrl(pathname, search) {
  const params = new URLSearchParams(search || '');
  params.set('canvas_inner', '1');
  return pathname + '?' + params.toString();
}

function postHeight() {
  const height = Math.ceil(document.documentElement.scrollHeight);
  window.parent?.postMessage({ type: 'app-canvas-height', height }, '*');
}

// 内层完整应用（iframe 固定 1440 视口内运行）
function CanvasApp() {
  const location = useLocation();

  useEffect(() => {
    const report = () => {
      postHeight();
      window.parent?.postMessage(
        { type: 'app-canvas-path', path: location.pathname },
        '*'
      );
    };
    report();
    const observer = new ResizeObserver(postHeight);
    observer.observe(document.body);
    return () => observer.disconnect();
  }, [location]);

  return (
    <>
      <Header />
      <Container className={'main-content'}>
        <App />
      </Container>
      <ToastContainer />
      <Footer />
    </>
  );
}

// 外层外壳：固定 1440 视口的 iframe + 等比缩放
function AppViewport() {
  const frameRef = useRef(null);
  const [scale, setScale] = useState(getFrameScale);
  const [frameHeight, setFrameHeight] = useState(window.innerHeight);
  const location = useLocation();

  useEffect(() => {
    const handleResize = () => setScale(getFrameScale());
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  useEffect(() => {
    document.body.classList.add('canvas-frame-mode');
    return () => document.body.classList.remove('canvas-frame-mode');
  }, []);

  // 父路由变化时同步 iframe 地址
  useEffect(() => {
    const frame = frameRef.current;
    if (!frame?.contentWindow) return;
    const innerPath = frame.contentWindow.location.pathname || '/';
    if (innerPath !== location.pathname) {
      frame.src = innerUrl(location.pathname, location.search);
    }
  }, [location]);

  // 接收 iframe 内部的高度与路由消息
  useEffect(() => {
    const handleMessage = (event) => {
      if (!event.data || typeof event.data !== 'object') return;
      if (event.data.type === 'app-canvas-height') {
        setFrameHeight(event.data.height);
      } else if (event.data.type === 'app-canvas-path') {
        const path = event.data.path;
        if (path && path !== window.location.pathname) {
          window.history.replaceState({}, '', path);
        }
      }
    };
    window.addEventListener('message', handleMessage);
    return () => window.removeEventListener('message', handleMessage);
  }, []);

  return (
    <div className='app-viewport' style={{ height: `${frameHeight * scale}px` }}>
      <iframe
        ref={frameRef}
        title='app-canvas'
        className='app-canvas-frame'
        src={innerUrl(location.pathname, location.search)}
        style={{
          width: `${CANVAS_WIDTH}px`,
          height: `${frameHeight}px`,
          transform: `scale(${scale})`,
          transformOrigin: 'top left',
        }}
        frameBorder='0'
      />
    </div>
  );
}

const root = ReactDOM.createRoot(document.getElementById('root'));
root.render(
  <React.StrictMode>
    <StatusProvider>
      <UserProvider>
        <BrowserRouter>
          {IS_INNER ? <CanvasApp /> : <AppViewport />}
        </BrowserRouter>
      </UserProvider>
    </StatusProvider>
  </React.StrictMode>
);
