import { showError } from './utils';
import { APP_SERVER } from '../constants';
import axios from 'axios';

export const API = axios.create({
  baseURL: APP_SERVER,
});

API.interceptors.response.use(
  (response) => response,
  (error) => {
    showError(error);
  }
);
