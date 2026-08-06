// 宏观版本/仓库信息 —— UI 各处显示的唯一引用点
// 唯一版本源：仓库根目录 VERSION 文件；构建时由 Dockerfile / web/build.sh
// 注入为 REACT_APP_VERSION（REACT_APP_VERSION=$(cat ./VERSION)）。
// 因此发布新版本只需改根 VERSION 一个文件，这里及所有组件无需改动。
// 仓库 URL 也统一在此，避免散落硬编码导致升级检查指向错误仓库。
export const APP_VERSION = process.env.REACT_APP_VERSION || '';
export const APP_REPO_URL = 'https://github.com/Dytchem/one-api';
export const APP_REPO_API_URL = 'https://api.github.com/repos/Dytchem/one-api';
export const APP_REPO_RELEASES_URL = `${APP_REPO_URL}/releases`;
export const APP_AUTHOR_URL = 'https://github.com/Dytchem';
export const APP_SERVER = process.env.REACT_APP_SERVER || '';
